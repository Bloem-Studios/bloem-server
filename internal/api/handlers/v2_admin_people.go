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
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type V2AdminPeopleService interface {
	List(context.Context, uuid.UUID, adminpeople.Filter) (adminpeople.Page, error)
	Get(context.Context, uuid.UUID, int) (adminpeople.PersonSummary, error)
	CreateSelection(context.Context, uuid.UUID, adminpeople.Filter) (adminpeople.Selection, error)
	PreviewPolicy(context.Context, uuid.UUID, int, string, adminpeople.PolicyCommand) (adminpeople.PolicyPreview, error)
	EnqueuePolicyBulk(context.Context, uuid.UUID, int, adminpeople.PolicyBulkAction) (adminpeople.BulkResult, error)
	ExecuteBulk(context.Context, uuid.UUID, int, adminpeople.BulkAction) (adminpeople.BulkResult, error)
	CancelBulkJob(context.Context, uuid.UUID, int, string) (adminpeople.BulkResult, error)
	CancelPolicyBulkJob(context.Context, uuid.UUID, int, string) (adminpeople.BulkResult, error)
	GetBulkJob(context.Context, uuid.UUID, string) (adminpeople.BulkResult, error)
	GetPolicyBulkJob(context.Context, uuid.UUID, string) (adminpeople.BulkResult, error)
	UpdateMembership(context.Context, uuid.UUID, int, int, int64, tenancy.MembershipStatus) (adminpeople.PersonSummary, error)
	UpdateProfileGroup(context.Context, uuid.UUID, int, int, string, int64, int) (adminpeople.PersonSummary, error)
}

type AdminPeopleWorkerWake interface{ Wake() }

type V2AdminPeopleCohortStore interface {
	ListCohorts(context.Context, uuid.UUID, bool) ([]entitlements.CohortRevision, error)
	GetCohort(context.Context, uuid.UUID, uuid.UUID) (entitlements.CohortRevision, error)
}

type V2AdminPeopleHandler struct {
	service     V2AdminPeopleService
	worker      AdminPeopleWorkerWake
	cohortStore V2AdminPeopleCohortStore
}

func NewV2AdminPeopleHandler(service V2AdminPeopleService) *V2AdminPeopleHandler {
	return &V2AdminPeopleHandler{service: service}
}
func NewV2AdminPeopleHandlerWithWake(service V2AdminPeopleService, worker AdminPeopleWorkerWake) *V2AdminPeopleHandler {
	return &V2AdminPeopleHandler{service: service, worker: worker}
}

func (h *V2AdminPeopleHandler) SetCohortStore(store V2AdminPeopleCohortStore) {
	if h != nil {
		h.cohortStore = store
	}
}

func (h *V2AdminPeopleHandler) HandleListEntitlementCohorts(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	if h.cohortStore == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}
	query := r.URL.Query()
	for key := range query {
		if key != "include_archived" {
			writeAdminValidation(w, map[string]string{"query": "contains unsupported parameters"})
			return
		}
	}
	includeArchived := false
	if raw := strings.TrimSpace(query.Get("include_archived")); raw != "" {
		if raw != platformEntitlementBulkTrue && raw != platformEntitlementBulkFalse {
			writeAdminValidation(w, map[string]string{"include_archived": "must be true or false"})
			return
		}
		includeArchived = raw == platformEntitlementBulkTrue
	}
	items, err := h.cohortStore.ListCohorts(r.Context(), tenant.OrganizationID, includeArchived)
	if err != nil {
		h.writeCohortError(w, err)
		return
	}
	cohorts := make([]platformEntitlementCohort, 0, len(items))
	for _, item := range items {
		if item.OrganizationID != tenant.OrganizationID {
			h.writeCohortError(w, errors.New("cohort store returned a foreign organization"))
			return
		}
		cohorts = append(cohorts, platformEntitlementCohortFromDomain(item))
	}
	writeJSON(w, http.StatusOK, struct {
		Cohorts []platformEntitlementCohort `json:"cohorts"`
	}{Cohorts: cohorts})
}

func (h *V2AdminPeopleHandler) HandleGetEntitlementCohort(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	if h.cohortStore == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}
	cohortID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "cohort_id")))
	if err != nil || cohortID == uuid.Nil {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	item, err := h.cohortStore.GetCohort(r.Context(), tenant.OrganizationID, cohortID)
	if err != nil || item.ID != cohortID || item.OrganizationID != tenant.OrganizationID {
		if err == nil {
			err = entitlements.ErrCohortNotFound
		}
		h.writeCohortError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Cohort platformEntitlementCohort `json:"cohort"`
	}{Cohort: platformEntitlementCohortFromDomain(item)})
}

func (h *V2AdminPeopleHandler) writeCohortError(w http.ResponseWriter, err error) {
	if errors.Is(err, entitlements.ErrCohortNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
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

func (h *V2AdminPeopleHandler) HandleCreatePolicyPreview(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	var request struct {
		SelectionToken string                    `json:"selection_token"`
		Command        adminpeople.PolicyCommand `json:"command"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	ctx := adminPeopleMutationContext(r)
	preview, err := h.service.PreviewPolicy(ctx, tenant.OrganizationID, tenant.AccountID, request.SelectionToken, request.Command)
	if err != nil {
		h.writeError(w, r, err, 0)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Preview adminpeople.PolicyPreview `json:"preview"`
	}{preview})
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

func (h *V2AdminPeopleHandler) HandleCreatePolicyJob(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	var action adminpeople.PolicyBulkAction
	if !decodeAdminPlatformJSON(w, r, &action) {
		return
	}
	result, err := h.service.EnqueuePolicyBulk(adminPeopleMutationContext(r), tenant.OrganizationID, tenant.AccountID, action)
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

func (h *V2AdminPeopleHandler) HandleCancelPolicyJob(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	jobID := strings.TrimSpace(chi.URLParam(r, "job_id"))
	if jobID == "" {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	result, err := h.service.CancelPolicyBulkJob(adminPeopleMutationContext(r), tenant.OrganizationID, tenant.AccountID, jobID)
	if err != nil {
		h.writeError(w, r, err, 0)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Job adminpeople.BulkResult `json:"job"`
	}{result})
}

func (h *V2AdminPeopleHandler) HandleGetPolicyJob(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	jobID := strings.TrimSpace(chi.URLParam(r, "job_id"))
	if jobID == "" {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	result, err := h.service.GetPolicyBulkJob(r.Context(), tenant.OrganizationID, jobID)
	if err != nil {
		h.writeError(w, r, err, 0)
		return
	}
	writeJSON(w, http.StatusOK, struct {
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
	case errors.Is(err, adminpeople.ErrInvalidBulkAction), errors.Is(err, adminpeople.ErrInvalidPolicyCommand):
		writeAdminValidation(w, map[string]string{"request": "contains an invalid people mutation"})
	case errors.Is(err, adminpeople.ErrInvalidPolicyConfirmation):
		writeError(w, http.StatusConflict, "policy_confirmation_stale", "The policy preview changed or expired; create a new preview")
	case errors.Is(err, adminpeople.ErrBulkIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", "The idempotency key was used for a different command")
	case errors.Is(err, adminpeople.ErrBulkJobNotCancellable):
		writeError(w, http.StatusConflict, "job_not_cancellable", "The requested job cannot be cancelled through this endpoint")
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
