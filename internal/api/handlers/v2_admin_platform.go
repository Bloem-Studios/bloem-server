package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/activitylog"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

const adminPlatformBodyLimit = 32 << 10

var organizationSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type V2AdminPlatformStore interface {
	ListOrganizations(context.Context, tenancy.OrganizationFilter) (tenancy.OrganizationPage, error)
	GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error)
	GetOrganizationSummary(context.Context, uuid.UUID) (tenancy.OrganizationSummary, error)
	CreateOrganization(context.Context, tenancy.CreateOrganizationInput) (tenancy.Organization, error)
	UpdateOrganization(context.Context, uuid.UUID, int64, tenancy.UpdateOrganizationInput) (tenancy.Organization, error)
	SetOrganizationStatus(context.Context, uuid.UUID, int64, tenancy.OrganizationStatus) (tenancy.Organization, error)
	TransferOwnership(context.Context, uuid.UUID, int64, int) (tenancy.Organization, error)
	ListOrganizationMemberships(context.Context, uuid.UUID, tenancy.MembershipFilter) (tenancy.MembershipPage, error)
	GetOrganizationMembership(context.Context, uuid.UUID, uuid.UUID) (tenancy.Membership, error)
	CreateMembership(context.Context, uuid.UUID, int64, tenancy.CreateMembershipInput) (tenancy.Membership, tenancy.Organization, error)
	UpdateMembership(context.Context, uuid.UUID, uuid.UUID, int64, tenancy.UpdateMembershipInput) (tenancy.Membership, tenancy.Organization, error)
}

type AdminEventRecorder interface {
	RecordAdminEvent(context.Context, activitylog.AdminEvent) error
}

type V2AdminPlatformHandler struct {
	store V2AdminPlatformStore
	audit AdminEventRecorder
}

func NewV2AdminPlatformHandler(store V2AdminPlatformStore, audit AdminEventRecorder) *V2AdminPlatformHandler {
	return &V2AdminPlatformHandler{store: store, audit: audit}
}

func (h *V2AdminPlatformHandler) HandleListOrganizations(w http.ResponseWriter, r *http.Request) {
	if !h.requirePlatform(w, r) {
		return
	}
	filter := tenancy.OrganizationFilter{Query: strings.TrimSpace(r.URL.Query().Get("query")), Cursor: strings.TrimSpace(r.URL.Query().Get("cursor"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > 200 {
			writeAdminValidation(w, map[string]string{"limit": "must be between 1 and 200"})
			return
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		status := tenancy.OrganizationStatus(raw)
		if status != tenancy.OrganizationInitializing && status != tenancy.OrganizationActive && status != tenancy.OrganizationSuspended {
			writeAdminValidation(w, map[string]string{"status": "must be initializing, active, or suspended"})
			return
		}
		filter.Status = &status
	}
	page, err := h.store.ListOrganizations(r.Context(), filter)
	if err != nil {
		h.writeStoreError(w, r, err, uuid.Nil, uuid.Nil)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Organizations []tenancy.OrganizationSummary `json:"organizations"`
		NextCursor    string                        `json:"next_cursor,omitempty"`
	}{page.Items, page.NextCursor})
}

func (h *V2AdminPlatformHandler) HandleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	var request struct {
		Name           string `json:"name"`
		Slug           string `json:"slug"`
		OwnerAccountID int    `json:"owner_account_id"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	fields := validateOrganizationFields(request.Name, request.Slug)
	if request.OwnerAccountID <= 0 {
		fields["owner_account_id"] = "must identify an account"
	}
	if len(fields) > 0 {
		writeAdminValidation(w, fields)
		return
	}
	created, err := h.store.CreateOrganization(r.Context(), tenancy.CreateOrganizationInput{
		Name: request.Name, Slug: request.Slug, OwnerAccountID: request.OwnerAccountID,
	})
	if err != nil {
		h.writeStoreError(w, r, err, uuid.Nil, uuid.Nil)
		return
	}
	event := h.organizationEvent(r, claims, "organization.created", created, 0, created.PolicyRevision, nil, organizationAuditState(created))
	if !h.recordAudit(w, r, event) {
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Organization tenancy.Organization `json:"organization"`
	}{created})
}

func (h *V2AdminPlatformHandler) HandleGetOrganization(w http.ResponseWriter, r *http.Request) {
	if !h.requirePlatform(w, r) {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	organization, err := h.store.GetOrganizationSummary(r.Context(), organizationID)
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, uuid.Nil)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Organization tenancy.OrganizationSummary `json:"organization"`
	}{organization})
}

func (h *V2AdminPlatformHandler) HandleUpdateOrganization(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64   `json:"expected_revision"`
		Name             *string `json:"name"`
		Slug             *string `json:"slug"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	fields := map[string]string{}
	if request.ExpectedRevision <= 0 {
		fields["expected_revision"] = "must be a positive revision"
	}
	if request.Name == nil && request.Slug == nil {
		fields["organization"] = "must include name or slug"
	}
	if request.Name != nil && strings.TrimSpace(*request.Name) == "" {
		fields["name"] = "must not be empty"
	}
	if request.Slug != nil && !organizationSlugPattern.MatchString(*request.Slug) {
		fields["slug"] = "must contain lowercase letters, numbers, and single hyphens"
	}
	if len(fields) > 0 {
		writeAdminValidation(w, fields)
		return
	}
	before, err := h.store.GetOrganization(r.Context(), organizationID)
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, uuid.Nil)
		return
	}
	after, err := h.store.UpdateOrganization(r.Context(), organizationID, request.ExpectedRevision, tenancy.UpdateOrganizationInput{Name: request.Name, Slug: request.Slug})
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, uuid.Nil)
		return
	}
	if before.Name != after.Name && !h.recordAudit(w, r, h.organizationEvent(r, claims, "organization.renamed", after, before.PolicyRevision, after.PolicyRevision, organizationAuditState(before), organizationAuditState(after))) {
		return
	}
	if before.Slug != after.Slug && !h.recordAudit(w, r, h.organizationEvent(r, claims, "organization.slug_changed", after, before.PolicyRevision, after.PolicyRevision, organizationAuditState(before), organizationAuditState(after))) {
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Organization tenancy.Organization `json:"organization"`
	}{after})
}

func (h *V2AdminPlatformHandler) HandleSuspendOrganization(w http.ResponseWriter, r *http.Request) {
	h.handleOrganizationStatus(w, r, tenancy.OrganizationSuspended, "organization.suspended")
}

func (h *V2AdminPlatformHandler) HandleReactivateOrganization(w http.ResponseWriter, r *http.Request) {
	h.handleOrganizationStatus(w, r, tenancy.OrganizationActive, "organization.reactivated")
}

func (h *V2AdminPlatformHandler) handleOrganizationStatus(w http.ResponseWriter, r *http.Request, status tenancy.OrganizationStatus, action string) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if request.ExpectedRevision <= 0 {
		writeAdminValidation(w, map[string]string{"expected_revision": "must be a positive revision"})
		return
	}
	before, err := h.store.GetOrganization(r.Context(), organizationID)
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, uuid.Nil)
		return
	}
	after, err := h.store.SetOrganizationStatus(r.Context(), organizationID, request.ExpectedRevision, status)
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, uuid.Nil)
		return
	}
	if !h.recordAudit(w, r, h.organizationEvent(r, claims, action, after, before.PolicyRevision, after.PolicyRevision, organizationAuditState(before), organizationAuditState(after))) {
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Organization tenancy.Organization `json:"organization"`
	}{after})
}

func (h *V2AdminPlatformHandler) HandleTransferOwnership(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64 `json:"expected_revision"`
		OwnerAccountID   int   `json:"owner_account_id"`
		Confirmed        bool  `json:"confirmed"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	fields := map[string]string{}
	if request.ExpectedRevision <= 0 {
		fields["expected_revision"] = "must be a positive revision"
	}
	if request.OwnerAccountID <= 0 {
		fields["owner_account_id"] = "must identify an account"
	}
	if !request.Confirmed {
		fields["confirmed"] = "must explicitly confirm ownership transfer"
	}
	if len(fields) > 0 {
		writeAdminValidation(w, fields)
		return
	}
	before, err := h.store.GetOrganization(r.Context(), organizationID)
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, uuid.Nil)
		return
	}
	after, err := h.store.TransferOwnership(r.Context(), organizationID, request.ExpectedRevision, request.OwnerAccountID)
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, uuid.Nil)
		return
	}
	if !h.recordAudit(w, r, h.organizationEvent(r, claims, "organization.ownership_transferred", after, before.PolicyRevision, after.PolicyRevision, organizationAuditState(before), organizationAuditState(after))) {
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Organization tenancy.Organization `json:"organization"`
	}{after})
}

func (h *V2AdminPlatformHandler) HandleListMemberships(w http.ResponseWriter, r *http.Request) {
	if !h.requirePlatform(w, r) {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	filter := tenancy.MembershipFilter{Cursor: strings.TrimSpace(r.URL.Query().Get("cursor"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > 200 {
			writeAdminValidation(w, map[string]string{"limit": "must be between 1 and 200"})
			return
		}
		filter.Limit = limit
	}
	page, err := h.store.ListOrganizationMemberships(r.Context(), organizationID, filter)
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, uuid.Nil)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Memberships []tenancy.MembershipSummary `json:"memberships"`
		NextCursor  string                      `json:"next_cursor,omitempty"`
	}{page.Items, page.NextCursor})
}

func (h *V2AdminPlatformHandler) HandleCreateMembership(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64                    `json:"expected_revision"`
		AccountID        int                      `json:"account_id"`
		LegacyRole       string                   `json:"legacy_role"`
		Status           tenancy.MembershipStatus `json:"status,omitempty"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	fields := map[string]string{}
	if request.ExpectedRevision <= 0 {
		fields["expected_revision"] = "must be a positive revision"
	}
	if request.AccountID <= 0 {
		fields["account_id"] = "must identify an account"
	}
	if request.LegacyRole != "admin" && request.LegacyRole != "user" {
		fields["legacy_role"] = "must be admin or user"
	}
	if request.Status != "" && request.Status != tenancy.MembershipActive && request.Status != tenancy.MembershipInvited && request.Status != tenancy.MembershipSuspended {
		fields["status"] = "must be invited, active, or suspended"
	}
	if len(fields) > 0 {
		writeAdminValidation(w, fields)
		return
	}
	membership, organization, err := h.store.CreateMembership(r.Context(), organizationID, request.ExpectedRevision, tenancy.CreateMembershipInput{AccountID: request.AccountID, LegacyRole: request.LegacyRole, Status: request.Status})
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, uuid.Nil)
		return
	}
	event := h.membershipEvent(r, claims, "membership.created", organization, membership, 0, membership.SecurityRevision, nil, membershipAuditState(membership))
	if !h.recordAudit(w, r, event) {
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Membership   tenancy.Membership   `json:"membership"`
		Organization tenancy.Organization `json:"organization"`
	}{membership, organization})
}

func (h *V2AdminPlatformHandler) HandleUpdateMembership(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.requirePlatformMutation(w, r)
	if !ok {
		return
	}
	organizationID, ok := adminPlatformPathUUID(w, r, "id")
	if !ok {
		return
	}
	membershipID, ok := adminPlatformPathUUID(w, r, "membership_id")
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64                     `json:"expected_revision"`
		LegacyRole       *string                   `json:"legacy_role"`
		Status           *tenancy.MembershipStatus `json:"status"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	fields := map[string]string{}
	if request.ExpectedRevision <= 0 {
		fields["expected_revision"] = "must be a positive revision"
	}
	if request.LegacyRole == nil && request.Status == nil {
		fields["membership"] = "must include legacy_role or status"
	}
	if request.LegacyRole != nil && *request.LegacyRole != "admin" && *request.LegacyRole != "user" {
		fields["legacy_role"] = "must be admin or user"
	}
	if request.Status != nil && *request.Status != tenancy.MembershipActive && *request.Status != tenancy.MembershipInvited && *request.Status != tenancy.MembershipSuspended {
		fields["status"] = "must be invited, active, or suspended"
	}
	if len(fields) > 0 {
		writeAdminValidation(w, fields)
		return
	}
	before, err := h.store.GetOrganizationMembership(r.Context(), organizationID, membershipID)
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, membershipID)
		return
	}
	after, organization, err := h.store.UpdateMembership(r.Context(), organizationID, membershipID, request.ExpectedRevision, tenancy.UpdateMembershipInput{LegacyRole: request.LegacyRole, Status: request.Status})
	if err != nil {
		h.writeStoreError(w, r, err, organizationID, membershipID)
		return
	}
	action := "membership.role_changed"
	if before.Status != after.Status {
		action = "membership.status_changed"
	}
	event := h.membershipEvent(r, claims, action, organization, after, before.SecurityRevision, after.SecurityRevision, membershipAuditState(before), membershipAuditState(after))
	if !h.recordAudit(w, r, event) {
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Membership   tenancy.Membership   `json:"membership"`
		Organization tenancy.Organization `json:"organization"`
	}{after, organization})
}

func (h *V2AdminPlatformHandler) requirePlatform(w http.ResponseWriter, r *http.Request) bool {
	claims, ok := middleware.GetAdminContextClaims(r.Context())
	if !ok || claims.Scope != auth.AdminScopePlatform || claims.AccountID <= 0 {
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
		return false
	}
	if h == nil || h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return false
	}
	return true
}

func (h *V2AdminPlatformHandler) requirePlatformMutation(w http.ResponseWriter, r *http.Request) (auth.AdminContextClaims, bool) {
	if !h.requirePlatform(w, r) {
		return auth.AdminContextClaims{}, false
	}
	claims, _ := middleware.GetAdminContextClaims(r.Context())
	if h.audit == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Administrative audit is unavailable")
		return auth.AdminContextClaims{}, false
	}
	return claims, true
}

func (h *V2AdminPlatformHandler) recordAudit(w http.ResponseWriter, r *http.Request, event activitylog.AdminEvent) bool {
	if err := h.audit.RecordAdminEvent(r.Context(), event); err != nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Administrative audit is unavailable")
		return false
	}
	return true
}

func (h *V2AdminPlatformHandler) writeStoreError(w http.ResponseWriter, r *http.Request, err error, organizationID, membershipID uuid.UUID) {
	switch {
	case errors.Is(err, tenancy.ErrOrganizationNotFound), errors.Is(err, tenancy.ErrMembershipNotFound), errors.Is(err, tenancy.ErrTenantNotFoundOrHidden):
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
	case errors.Is(err, tenancy.ErrAuthorizationStateChanged):
		var revision int64
		if membershipID != uuid.Nil {
			if current, loadErr := h.store.GetOrganizationMembership(r.Context(), organizationID, membershipID); loadErr == nil || current.SecurityRevision > 0 {
				revision = current.SecurityRevision
			}
		} else if organizationID != uuid.Nil {
			if current, loadErr := h.store.GetOrganization(r.Context(), organizationID); loadErr == nil || current.PolicyRevision > 0 {
				revision = current.PolicyRevision
			}
		}
		writeJSON(w, http.StatusConflict, struct {
			Error           string `json:"error"`
			Message         string `json:"message"`
			CurrentRevision int64  `json:"current_revision"`
		}{"authorization_state_changed", "Authorization state changed; reload and retry", revision})
	case errors.Is(err, tenancy.ErrOrganizationSlugConflict):
		writeError(w, http.StatusConflict, "organization_slug_conflict", "Organization slug is already in use")
	case errors.Is(err, tenancy.ErrMembershipConflict):
		writeError(w, http.StatusConflict, "membership_conflict", "Membership conflicts with protected organization state")
	case errors.Is(err, tenancy.ErrOwnerNotEligible):
		writeAdminValidation(w, map[string]string{"owner_account_id": "must identify an enabled organization member"})
	case errors.Is(err, tenancy.ErrInvalidCursor):
		writeAdminValidation(w, map[string]string{"cursor": "is invalid"})
	case errors.Is(err, tenancy.ErrInvalidAdminMutation):
		writeAdminValidation(w, map[string]string{"request": "contains invalid lifecycle state"})
	default:
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
	}
}

func decodeAdminPlatformJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminPlatformBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid administrative request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid administrative request")
		return false
	}
	return true
}

func adminPlatformPathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	value, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return uuid.Nil, false
	}
	return value, true
}

func validateOrganizationFields(name, slug string) map[string]string {
	fields := map[string]string{}
	if strings.TrimSpace(name) == "" {
		fields["name"] = "must not be empty"
	}
	if !organizationSlugPattern.MatchString(slug) {
		fields["slug"] = "must contain lowercase letters, numbers, and single hyphens"
	}
	return fields
}

func writeAdminValidation(w http.ResponseWriter, fields map[string]string) {
	writeJSON(w, http.StatusUnprocessableEntity, struct {
		Error   string            `json:"error"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	}{"validation_failed", "Administrative request validation failed", fields})
}

func (h *V2AdminPlatformHandler) organizationEvent(r *http.Request, claims auth.AdminContextClaims, action string, organization tenancy.Organization, beforeRevision, afterRevision int64, beforeState, afterState map[string]any) activitylog.AdminEvent {
	return activitylog.AdminEvent{
		ActorAccountID: claims.AccountID, ActorPlatformRole: "platform_admin", AuthorityContext: "platform",
		Action: action, TargetType: "organization", TargetID: organization.ID.String(), OrganizationID: organization.ID,
		BeforeRevision: beforeRevision, AfterRevision: afterRevision, Outcome: "success", RequestID: adminRequestID(r),
		BeforeState: beforeState, AfterState: afterState,
	}
}

func (h *V2AdminPlatformHandler) membershipEvent(r *http.Request, claims auth.AdminContextClaims, action string, organization tenancy.Organization, membership tenancy.Membership, beforeRevision, afterRevision int64, beforeState, afterState map[string]any) activitylog.AdminEvent {
	return activitylog.AdminEvent{
		ActorAccountID: claims.AccountID, ActorPlatformRole: "platform_admin", AuthorityContext: "platform",
		Action: action, TargetType: "membership", TargetID: membership.ID.String(), OrganizationID: organization.ID,
		SubjectID: strconv.Itoa(membership.AccountID), BeforeRevision: beforeRevision, AfterRevision: afterRevision,
		Outcome: "success", RequestID: adminRequestID(r), BeforeState: beforeState, AfterState: afterState,
	}
}

func organizationAuditState(organization tenancy.Organization) map[string]any {
	state := map[string]any{"name": organization.Name, "slug": organization.Slug, "status": organization.Status}
	if organization.OwnerAccountID != nil {
		state["owner_account_id"] = *organization.OwnerAccountID
	}
	return state
}

func membershipAuditState(membership tenancy.Membership) map[string]any {
	return map[string]any{"account_id": membership.AccountID, "status": membership.Status, "legacy_role": membership.LegacyRole}
}

func adminRequestID(r *http.Request) string {
	if requestID := chimw.GetReqID(r.Context()); requestID != "" {
		return requestID
	}
	return strings.TrimSpace(r.Header.Get("X-Request-ID"))
}
