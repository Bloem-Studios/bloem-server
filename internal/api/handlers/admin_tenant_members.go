package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/apiresponse"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type tenantMemberService interface {
	Create(context.Context, uuid.UUID, string, tenancy.CreateMemberInput) (models.User, bool, error)
	List(context.Context, uuid.UUID) ([]models.User, error)
	Get(context.Context, uuid.UUID, int) (models.User, error)
	Update(context.Context, uuid.UUID, int, tenancy.UpdateMemberInput) (models.User, error)
	Suspend(context.Context, uuid.UUID, int) (models.User, error)
	Resume(context.Context, uuid.UUID, int) (models.User, error)
	ResetPassword(context.Context, uuid.UUID, int, string) (models.User, error)
	Delete(context.Context, uuid.UUID, int) error
	RequireMembership(context.Context, uuid.UUID, int) error
}

type tenantMemberLifecycleCreator interface {
	CreateInTransaction(context.Context, pgx.Tx, uuid.UUID, tenancy.CreateMemberInput) (models.User, uuid.UUID, error)
	CompleteCreateAfterCommit()
}

// AdminTenantMembersHandler serves the reseller member lifecycle and gates
// nested Task 4 resources on the asserted tenant membership.
type AdminTenantMembersHandler struct {
	members    tenantMemberService
	adminUsers *AdminHandler
	lifecycle  lifecycleidempotency.Coordinator
	digest     lifecycleidempotency.RequestDigester
}

func NewAdminTenantMembersHandler(members tenantMemberService, adminUsers *AdminHandler) *AdminTenantMembersHandler {
	return &AdminTenantMembersHandler{members: members, adminUsers: adminUsers}
}

// SetLifecycleIdempotency installs the durable coordinator used by tenant
// member lifecycle mutations.
func (h *AdminTenantMembersHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester) {
	h.lifecycle = coordinator
	h.digest = digester
}

// PurgeOrganizationResources adapts the full Task 4 profile lifecycle for a
// membership delete whose global account survives. It removes only profiles
// in the asserted organization; each deletion retains avatar cleanup, device
// library purging, settings cleanup, and native lifecycle events.
func (h *AdminTenantMembersHandler) PurgeOrganizationResources(ctx context.Context, organizationID uuid.UUID, userID int) error {
	if h == nil || h.adminUsers == nil || h.adminUsers.storeProv == nil {
		return errors.New("tenant member resource lifecycle is not configured")
	}
	store, err := h.adminUsers.storeProv.ForUser(ctx, userID)
	if err != nil {
		return err
	}
	profiles, err := store.ListProfiles(ctx)
	if err != nil {
		return err
	}
	profileHandler := h.adminUsers.adminResourceProfileHandler()
	for i := range profiles {
		profile := profiles[i]
		if profile.OrganizationID != organizationID.String() {
			continue
		}
		if err := profileHandler.deleteProfileWithLifecycle(ctx, store, userID, &profile); err != nil {
			return err
		}
	}
	return nil
}

type tenantMemberResponse struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Status   string `json:"status"`
}

const (
	tenantMemberStatusActive    = "active"
	tenantMemberStatusSuspended = "suspended"
)

func toTenantMemberResponse(user models.User) tenantMemberResponse {
	status := tenantMemberStatusActive
	if !user.Enabled {
		status = tenantMemberStatusSuspended
	}
	return tenantMemberResponse{UserID: user.ID, Username: user.Username, Email: user.Email, Status: status}
}

func (h *AdminTenantMembersHandler) tenantID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "tenant_id")))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such tenant member")
		return uuid.Nil, false
	}
	return id, true
}

func (h *AdminTenantMembersHandler) memberScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, int, bool) {
	tenantID, ok := h.tenantID(w, r)
	if !ok {
		return uuid.Nil, 0, false
	}
	userID, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "user_id")))
	if err != nil || userID <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "No such tenant member")
		return uuid.Nil, 0, false
	}
	return tenantID, userID, true
}

func (h *AdminTenantMembersHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	members, err := h.members.List(r.Context(), tenantID)
	if err != nil {
		h.writeError(w, err, "Failed to list tenant members")
		return
	}
	out := make([]tenantMemberResponse, 0, len(members))
	for _, member := range members {
		out = append(out, toTenantMemberResponse(member))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *AdminTenantMembersHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	input := tenancy.CreateMemberInput{Username: req.Username, Email: req.Email, Password: req.Password}
	if h.lifecycle != nil && h.digest != nil {
		h.handleLifecycleCreate(w, r, tenantID, input, raw)
		return
	}
	if key == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required")
		return
	}
	member, replay, err := h.members.Create(r.Context(), tenantID, key, tenancy.CreateMemberInput{
		Username: req.Username, Email: req.Email, Password: req.Password,
	})
	if err != nil {
		h.writeError(w, err, "Failed to create tenant member")
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	writeJSON(w, status, toTenantMemberResponse(member))
}

func (h *AdminTenantMembersHandler) handleLifecycleCreate(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID, input tenancy.CreateMemberInput, body []byte) {
	creator, ok := h.members.(tenantMemberLifecycleCreator)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	actorID, actorIncarnation, ok := lifecycleActor(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authenticated account identity is incomplete")
		return
	}
	routeID := "tenant.member.create"
	request := lifecycleidempotency.Request{IdempotencyKey: r.Header.Get("Idempotency-Key"), Binding: lifecycleidempotency.Binding{
		ActorKind: lifecycleidempotency.ActorAuthenticatedAccount, ActorAccountID: &actorID, ActorAccountIncarnationID: &actorIncarnation,
		Method: r.Method, RouteID: routeID, RequestHash: h.digest(r.Method, routeID, map[string]string{"tenant_id": tenantID.String()}, r.URL.Query(), body),
		TargetSource: lifecycleidempotency.TargetBodyAccount,
	}}
	result, err := h.lifecycle.ExecuteCreate(r.Context(), request, func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, lifecycleidempotency.Result, error) {
		member, membershipID, err := creator.CreateInTransaction(ctx, tx, tenantID, input)
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		created, err := memberLifecycleJSONResult(http.StatusCreated, toTenantMemberResponse(member))
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		return []lifecycleidempotency.TargetBinding{{OrganizationID: tenantID, MembershipID: membershipID, AccountID: member.ID, AccountIncarnationID: member.AccountIncarnationID}}, created, nil
	})
	if err != nil {
		h.writeError(w, err, "Failed to create tenant member")
		return
	}
	if !result.Replayed {
		creator.CompleteCreateAfterCommit()
	}
	writeLifecycleResult(w, result)
}

func (h *AdminTenantMembersHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	h.withMember(w, r, func(member models.User) { writeJSON(w, http.StatusOK, toTenantMemberResponse(member)) })
}

func (h *AdminTenantMembersHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := h.memberScope(w, r)
	if !ok {
		return
	}
	var req struct {
		Username *string `json:"username"`
		Email    *string `json:"email"`
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || json.NewDecoder(bytes.NewReader(raw)).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if h.lifecycle != nil && h.digest != nil {
		h.handleLifecycleUpdate(w, r, tenantID, userID, req.Username, req.Email, raw)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	member, err := h.members.Update(r.Context(), tenantID, userID, tenancy.UpdateMemberInput{
		Username: req.Username, Email: req.Email,
	})
	if err != nil {
		h.writeError(w, err, "Failed to update tenant member")
		return
	}
	writeJSON(w, http.StatusOK, toTenantMemberResponse(member))
}

func (h *AdminTenantMembersHandler) HandleSuspend(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.digest != nil {
		h.handleLifecycleState(w, r, true)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	h.changeMemberState(w, r, h.members.Suspend, "Failed to suspend tenant member")
}

func (h *AdminTenantMembersHandler) HandleResume(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.digest != nil {
		h.handleLifecycleState(w, r, false)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	h.changeMemberState(w, r, h.members.Resume, "Failed to resume tenant member")
}

func (h *AdminTenantMembersHandler) HandleResetPassword(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := h.memberScope(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || json.NewDecoder(bytes.NewReader(raw)).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if h.lifecycle != nil && h.digest != nil {
		h.handleLifecycleResetPassword(w, r, tenantID, userID, req.Password, raw)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	member, err := h.members.ResetPassword(r.Context(), tenantID, userID, req.Password)
	if err != nil {
		h.writeError(w, err, "Failed to reset tenant member password")
		return
	}
	writeJSON(w, http.StatusOK, toTenantMemberResponse(member))
}

func (h *AdminTenantMembersHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, ok := h.memberScope(w, r)
	if !ok {
		return
	}
	if h.lifecycle != nil && h.digest != nil {
		h.handleLifecycleDelete(w, r, tenantID, userID)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	if err := h.members.Delete(r.Context(), tenantID, userID); err != nil {
		h.writeError(w, err, "Failed to delete tenant member")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminTenantMembersHandler) withMember(w http.ResponseWriter, r *http.Request, fn func(models.User)) {
	tenantID, userID, ok := h.memberScope(w, r)
	if !ok {
		return
	}
	member, err := h.members.Get(r.Context(), tenantID, userID)
	if err != nil {
		h.writeError(w, err, "Failed to load tenant member")
		return
	}
	fn(member)
}

func (h *AdminTenantMembersHandler) changeMemberState(
	w http.ResponseWriter,
	r *http.Request,
	change func(context.Context, uuid.UUID, int) (models.User, error),
	fallback string,
) {
	tenantID, userID, ok := h.memberScope(w, r)
	if !ok {
		return
	}
	member, err := change(r.Context(), tenantID, userID)
	if err != nil {
		h.writeError(w, err, fallback)
		return
	}
	writeJSON(w, http.StatusOK, toTenantMemberResponse(member))
}

func (h *AdminTenantMembersHandler) delegateNested(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	tenantID, userID, ok := h.memberScope(w, r)
	if !ok {
		return
	}
	if err := h.members.RequireMembership(r.Context(), tenantID, userID); err != nil {
		h.writeError(w, err, "Failed to load tenant member")
		return
	}
	if h.adminUsers == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Tenant member resources are not configured")
		return
	}
	r = r.WithContext(withAdminResourceOrganization(r.Context(), tenantID))
	next(w, r)
}

func (h *AdminTenantMembersHandler) delegateNestedLifecycle(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	tenantID, _, ok := h.memberScope(w, r)
	if !ok {
		return
	}
	if h.adminUsers == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Tenant member resources are not configured")
		return
	}
	next(w, r.WithContext(withAdminResourceOrganization(r.Context(), tenantID)))
}

func (h *AdminTenantMembersHandler) HandleListProfiles(w http.ResponseWriter, r *http.Request) {
	h.delegateNested(w, r, h.adminUsers.HandleListUserProfiles)
}
func (h *AdminTenantMembersHandler) HandleCreateProfile(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.digest != nil {
		h.delegateNestedLifecycle(w, r, h.adminUsers.HandleCreateUserProfile)
		return
	}
	h.delegateNested(w, r, h.adminUsers.HandleCreateUserProfile)
}
func (h *AdminTenantMembersHandler) HandleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.digest != nil {
		h.delegateNestedLifecycle(w, r, h.adminUsers.HandleUpdateUserProfile)
		return
	}
	h.delegateNested(w, r, h.adminUsers.HandleUpdateUserProfile)
}
func (h *AdminTenantMembersHandler) HandleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.digest != nil {
		h.delegateNestedLifecycle(w, r, h.adminUsers.HandleDeleteUserProfile)
		return
	}
	h.delegateNested(w, r, h.adminUsers.HandleDeleteUserProfile)
}
func (h *AdminTenantMembersHandler) HandleListDevices(w http.ResponseWriter, r *http.Request) {
	h.delegateNested(w, r, h.adminUsers.HandleListUserDevices)
}
func (h *AdminTenantMembersHandler) HandleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.digest != nil {
		h.delegateNestedLifecycle(w, r, h.adminUsers.HandleDeleteUserDevice)
		return
	}
	h.delegateNested(w, r, h.adminUsers.HandleDeleteUserDevice)
}
func (h *AdminTenantMembersHandler) HandleListAuthSessions(w http.ResponseWriter, r *http.Request) {
	h.delegateNested(w, r, h.adminUsers.HandleListUserAuthSessions)
}
func (h *AdminTenantMembersHandler) HandleRevokeAuthSession(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.digest != nil {
		h.handleLifecycleRevokeAuthSession(w, r)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	h.delegateNested(w, r, h.adminUsers.HandleRevokeUserAuthSession)
}
func (h *AdminTenantMembersHandler) HandleRevokeAllAuthSessions(w http.ResponseWriter, r *http.Request) {
	if h.lifecycle != nil && h.digest != nil {
		h.handleLifecycleRevokeAllAuthSessions(w, r)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}
	h.delegateNested(w, r, h.adminUsers.HandleRevokeAllUserAuthSessions)
}

func (h *AdminTenantMembersHandler) writeError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, lifecycleidempotency.ErrKeyRequired):
		writeError(w, http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required for this lifecycle mutation")
	case errors.Is(err, lifecycleidempotency.ErrKeyMalformed):
		writeError(w, http.StatusBadRequest, "idempotency_key_invalid", "Idempotency-Key must be a bounded opaque ASCII value")
	case errors.Is(err, lifecycleidempotency.ErrConflict):
		writeError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key conflicts with its original lifecycle request")
	case errors.Is(err, lifecycleidempotency.ErrPending):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "lifecycle_request_pending", "Lifecycle request completion is pending")
	case errors.Is(err, tenancy.ErrMemberNotFound), errors.Is(err, tenancy.ErrTenantOrganizationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such tenant member")
	case errors.Is(err, tenancy.ErrSlotQuotaExceeded), errors.Is(err, tenancy.ErrTenantFrozen):
		writeError(w, http.StatusUnprocessableEntity, "slot_quota_exceeded", "The tenant has no available member slot")
	case errors.Is(err, tenancy.ErrUsernameConflict):
		writeError(w, http.StatusConflict, "username_conflict", "The username or email is already in use")
	case errors.Is(err, tenancy.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", "The idempotency key was used for a different command")
	case errors.Is(err, tenancy.ErrInvalidMemberCommand):
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid tenant member request")
	case errors.Is(err, tenancy.ErrMembershipPolicyWriteUnavailable):
		apiresponse.WriteRetryableUnavailable(w, "membership_policy_rollout_pending", "Membership policy rollout is not ready for this mutation", 1)
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", fallback)
	}
}
