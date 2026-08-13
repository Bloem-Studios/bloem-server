package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type V2AdminPeopleService interface {
	List(context.Context, uuid.UUID, adminpeople.Filter) (adminpeople.Page, error)
	Get(context.Context, uuid.UUID, int) (adminpeople.PersonSummary, error)
	CreateSelection(context.Context, uuid.UUID, adminpeople.Filter) (adminpeople.Selection, error)
	ExecuteBulk(context.Context, uuid.UUID, int, adminpeople.BulkAction) (adminpeople.BulkResult, error)
	GetBulkJob(context.Context, uuid.UUID, string) (adminpeople.BulkResult, error)
	UpdateMembership(context.Context, uuid.UUID, int, int, int64, tenancy.MembershipStatus) (adminpeople.PersonSummary, error)
	UpdateProfileGroup(context.Context, uuid.UUID, int, int, string, int64, int) (adminpeople.PersonSummary, error)
}

type AdminPeopleWorkerWake interface{ Wake() }
type V2AdminPeopleHandler struct {
	service V2AdminPeopleService
	worker  AdminPeopleWorkerWake
}

func NewV2AdminPeopleHandler(service V2AdminPeopleService) *V2AdminPeopleHandler {
	return &V2AdminPeopleHandler{service: service}
}
func NewV2AdminPeopleHandlerWithWake(service V2AdminPeopleService, worker AdminPeopleWorkerWake) *V2AdminPeopleHandler {
	return &V2AdminPeopleHandler{service: service, worker: worker}
}

func (h *V2AdminPeopleHandler) HandleListPeople(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	filter, ok := adminPeopleFilterFromQuery(w, r)
	if !ok {
		return
	}
	page, err := h.service.List(r.Context(), tenant.OrganizationID, filter)
	if err != nil {
		h.writeError(w, r, err, 0)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *V2AdminPeopleHandler) HandleGetPerson(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	accountID, ok := adminPeoplePathAccount(w, r)
	if !ok {
		return
	}
	person, err := h.service.Get(r.Context(), tenant.OrganizationID, accountID)
	if err != nil {
		h.writeError(w, r, err, accountID)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Person adminpeople.PersonSummary `json:"person"`
	}{person})
}

type adminPeopleFilterRequest struct {
	Query       string                     `json:"query"`
	Status      []tenancy.MembershipStatus `json:"status"`
	GroupIDs    []int                      `json:"group_ids"`
	ActiveSince *time.Time                 `json:"active_since"`
	Sort        string                     `json:"sort"`
}

func (h *V2AdminPeopleHandler) HandleCreateSelection(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	var request adminPeopleFilterRequest
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	selection, err := h.service.CreateSelection(r.Context(), tenant.OrganizationID, adminpeople.Filter{Query: request.Query, Status: request.Status, GroupIDs: request.GroupIDs, ActiveSince: request.ActiveSince, Sort: request.Sort})
	if err != nil {
		h.writeError(w, r, err, 0)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Selection adminpeople.Selection `json:"selection"`
	}{selection})
}

func (h *V2AdminPeopleHandler) HandleCreateBulkJob(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	var action adminpeople.BulkAction
	if !decodeAdminPlatformJSON(w, r, &action) {
		return
	}
	ctx := adminPeopleMutationContext(r)
	result, err := h.service.ExecuteBulk(ctx, tenant.OrganizationID, tenant.AccountID, action)
	if err != nil {
		h.writeError(w, r, err, 0)
		return
	}
	if h.worker != nil {
		h.worker.Wake()
	}
	writeJSON(w, http.StatusCreated, struct {
		Job adminpeople.BulkResult `json:"job"`
	}{result})
}

func (h *V2AdminPeopleHandler) HandleGetBulkJob(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	jobID := strings.TrimSpace(chi.URLParam(r, "job_id"))
	if jobID == "" {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	result, err := h.service.GetBulkJob(r.Context(), tenant.OrganizationID, jobID)
	if err != nil {
		h.writeError(w, r, err, 0)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Job adminpeople.BulkResult `json:"job"`
	}{result})
}

func (h *V2AdminPeopleHandler) HandleUpdateMembership(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	accountID, ok := adminPeoplePathAccount(w, r)
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64                    `json:"expected_revision"`
		Status           tenancy.MembershipStatus `json:"status"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if request.ExpectedRevision <= 0 || (request.Status != tenancy.MembershipActive && request.Status != tenancy.MembershipSuspended) {
		writeAdminValidation(w, map[string]string{"request": "must include a current expected_revision and active or suspended status"})
		return
	}
	ctx := adminPeopleMutationContext(r)
	person, err := h.service.UpdateMembership(ctx, tenant.OrganizationID, tenant.AccountID, accountID, request.ExpectedRevision, request.Status)
	if err != nil {
		h.writeError(w, r, err, accountID)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Person adminpeople.PersonSummary `json:"person"`
	}{person})
}

func (h *V2AdminPeopleHandler) HandleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	accountID, ok := adminPeoplePathAccount(w, r)
	if !ok {
		return
	}
	profileID := strings.TrimSpace(chi.URLParam(r, "profile_id"))
	if profileID == "" {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	var request struct {
		ExpectedRevision int64 `json:"expected_revision"`
		GroupID          int   `json:"group_id"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if request.ExpectedRevision <= 0 || request.GroupID <= 0 {
		writeAdminValidation(w, map[string]string{"request": "must include a current expected_revision and group_id"})
		return
	}
	ctx := adminPeopleMutationContext(r)
	person, err := h.service.UpdateProfileGroup(ctx, tenant.OrganizationID, tenant.AccountID, accountID, profileID, request.ExpectedRevision, request.GroupID)
	if err != nil {
		h.writeError(w, r, err, accountID)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Person adminpeople.PersonSummary `json:"person"`
	}{person})
}

func (h *V2AdminPeopleHandler) requireOrganization(w http.ResponseWriter, r *http.Request) (tenancy.Context, bool) {
	claims, claimsOK := middleware.GetAdminContextClaims(r.Context())
	tenant, tenantOK := tenancy.FromContext(r.Context())
	if !claimsOK || !tenantOK || claims.Scope != auth.AdminScopeOrganization || claims.AccountID <= 0 || claims.OrganizationID == uuid.Nil || claims.AccountID != tenant.AccountID || claims.OrganizationID != tenant.OrganizationID || claims.MembershipID != tenant.MembershipID {
		writeError(w, http.StatusForbidden, "insufficient_organization_authority", "Organization administrator authority required")
		return tenancy.Context{}, false
	}
	if h == nil || h.service == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return tenancy.Context{}, false
	}
	return tenant, true
}

func (h *V2AdminPeopleHandler) writeError(w http.ResponseWriter, r *http.Request, err error, accountID int) {
	switch {
	case errors.Is(err, adminpeople.ErrNotFound), errors.Is(err, adminpeople.ErrInvalidSelection):
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
	case errors.Is(err, adminpeople.ErrSelectionExpired):
		writeError(w, http.StatusConflict, "selection_expired", "The immutable selection expired; create a new selection")
	case errors.Is(err, adminpeople.ErrAuthorizationStateChanged):
		var revision int64
		if accountID > 0 {
			tenant, _ := tenancy.FromContext(r.Context())
			person, loadErr := h.service.Get(r.Context(), tenant.OrganizationID, accountID)
			if loadErr != nil {
				writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
				return
			}
			revision = person.SecurityRevision
		}
		writeJSON(w, http.StatusConflict, struct {
			Error           string `json:"error"`
			Message         string `json:"message"`
			CurrentRevision int64  `json:"current_revision"`
		}{"authorization_state_changed", "Authorization state changed; reload and retry", revision})
	case errors.Is(err, adminpeople.ErrInvalidCursor):
		writeAdminValidation(w, map[string]string{"cursor": "is invalid"})
	case errors.Is(err, adminpeople.ErrInvalidFilter):
		writeAdminValidation(w, map[string]string{"filters": "contain invalid values"})
	case errors.Is(err, adminpeople.ErrInvalidBulkAction):
		writeAdminValidation(w, map[string]string{"request": "contains an invalid people mutation"})
	default:
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
	}
}

func adminPeoplePathAccount(w http.ResponseWriter, r *http.Request) (int, bool) {
	value, err := strconv.Atoi(chi.URLParam(r, "account_id"))
	if err != nil || value <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return 0, false
	}
	return value, true
}

func adminPeopleMutationContext(r *http.Request) context.Context {
	claims, _ := middleware.GetAdminContextClaims(r.Context())
	authority := claims.EffectiveAuthority
	if authority == "" {
		authority = adminpeople.AuthorityOrganizationAdmin
	}
	return adminpeople.WithMutationActor(r.Context(), adminpeople.MutationActor{AccountID: claims.AccountID, Authority: authority, MembershipID: claims.MembershipID, SecurityRevision: claims.SecurityRevision, PolicyRevision: claims.PolicyRevision, RequestID: adminRequestID(r)})
}

func adminPeopleFilterFromQuery(w http.ResponseWriter, r *http.Request) (adminpeople.Filter, bool) {
	query := r.URL.Query()
	filter := adminpeople.Filter{Query: strings.TrimSpace(query.Get("query")), Sort: strings.TrimSpace(query.Get("sort")), Cursor: strings.TrimSpace(query.Get("cursor"))}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > 200 {
			writeAdminValidation(w, map[string]string{"limit": "must be between 1 and 200"})
			return adminpeople.Filter{}, false
		}
		filter.Limit = limit
	}
	for _, raw := range query["status"] {
		for _, value := range strings.Split(raw, ",") {
			status := tenancy.MembershipStatus(strings.TrimSpace(value))
			if status != "" {
				filter.Status = append(filter.Status, status)
			}
		}
	}
	for _, raw := range query["group_id"] {
		for _, value := range strings.Split(raw, ",") {
			id, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || id <= 0 {
				writeAdminValidation(w, map[string]string{"group_id": "must contain positive identifiers"})
				return adminpeople.Filter{}, false
			}
			filter.GroupIDs = append(filter.GroupIDs, id)
		}
	}
	if raw := strings.TrimSpace(query.Get("active_since")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeAdminValidation(w, map[string]string{"active_since": "must be RFC3339"})
			return adminpeople.Filter{}, false
		}
		filter.ActiveSince = &value
	}
	return filter, true
}
