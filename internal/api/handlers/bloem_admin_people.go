package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type BloemAdminPeopleService interface {
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

type BloemAdminPeopleCohortStore interface {
	ListCohorts(context.Context, uuid.UUID, bool) ([]entitlements.CohortRevision, error)
	GetCohort(context.Context, uuid.UUID, uuid.UUID) (entitlements.CohortRevision, error)
}

type BloemAdminPeopleHandler struct {
	service         BloemAdminPeopleService
	worker          AdminPeopleWorkerWake
	cohortStore     BloemAdminPeopleCohortStore
	lifecycle       lifecycleidempotency.Coordinator
	lifecycleDigest lifecycleidempotency.RequestDigester
}

func (h *BloemAdminPeopleHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester) {
	h.lifecycle = coordinator
	h.lifecycleDigest = digester
}

type bloemAdminPeopleLifecycleService interface {
	UpdateMembershipInTransaction(context.Context, pgx.Tx, uuid.UUID, int, int, int64, tenancy.MembershipStatus) (adminpeople.PersonSummary, error)
	UpdateProfileGroupInTransaction(context.Context, pgx.Tx, uuid.UUID, int, int, string, int64, int) (adminpeople.PersonSummary, error)
}

type bloemAdminPeopleLifecycleJobService interface {
	EnqueueBulkInTransaction(context.Context, pgx.Tx, uuid.UUID, int, adminpeople.BulkAction) (adminpeople.BulkResult, error)
	EnqueuePolicyBulkForScopeInTransaction(context.Context, pgx.Tx, uuid.UUID, int, adminpeople.PolicyBulkAction, adminpeople.PolicyOperationScope) (adminpeople.BulkResult, error)
}

type bloemAdminPeopleLifecycleSelectionResolver interface {
	ResolveLifecycleSelectionTargets(context.Context, pgx.Tx, uuid.UUID, string) ([]lifecycleidempotency.TargetBinding, error)
}

func NewBloemAdminPeopleHandler(service BloemAdminPeopleService) *BloemAdminPeopleHandler {
	return &BloemAdminPeopleHandler{service: service}
}
func NewBloemAdminPeopleHandlerWithWake(service BloemAdminPeopleService, worker AdminPeopleWorkerWake) *BloemAdminPeopleHandler {
	return &BloemAdminPeopleHandler{service: service, worker: worker}
}

func (h *BloemAdminPeopleHandler) SetCohortStore(store BloemAdminPeopleCohortStore) {
	if h != nil {
		h.cohortStore = store
	}
}

func (h *BloemAdminPeopleHandler) HandleListEntitlementCohorts(w http.ResponseWriter, r *http.Request) {
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
		if key != entitlementIncludeArchivedParam {
			writeAdminValidation(w, map[string]string{entitlementQueryValidationField: "contains unsupported parameters"})
			return
		}
	}
	includeArchived := false
	if raw := strings.TrimSpace(query.Get(entitlementIncludeArchivedParam)); raw != "" {
		if raw != platformEntitlementBulkTrue && raw != platformEntitlementBulkFalse {
			writeAdminValidation(w, map[string]string{entitlementIncludeArchivedParam: "must be true or false"})
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

func (h *BloemAdminPeopleHandler) HandleGetEntitlementCohort(w http.ResponseWriter, r *http.Request) {
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

func (h *BloemAdminPeopleHandler) writeCohortError(w http.ResponseWriter, err error) {
	if errors.Is(err, entitlements.ErrCohortNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
}

func (h *BloemAdminPeopleHandler) HandleListPeople(w http.ResponseWriter, r *http.Request) {
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

func (h *BloemAdminPeopleHandler) HandleGetPerson(w http.ResponseWriter, r *http.Request) {
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

func (h *BloemAdminPeopleHandler) HandleCreateSelection(w http.ResponseWriter, r *http.Request) {
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

func (h *BloemAdminPeopleHandler) HandleCreatePolicyPreview(w http.ResponseWriter, r *http.Request) {
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

func (h *BloemAdminPeopleHandler) HandleCreateBulkJob(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	body, ok := captureBloemLifecycleBody(w, r)
	if !ok {
		return
	}
	var action adminpeople.BulkAction
	if !decodeAdminPlatformJSON(w, r, &action) {
		return
	}
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		h.handleLifecyclePeopleBulkJob(w, r, tenant, body, action)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
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

func (h *BloemAdminPeopleHandler) HandleCreatePolicyJob(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	body, ok := captureBloemLifecycleBody(w, r)
	if !ok {
		return
	}
	var action adminpeople.PolicyBulkAction
	if !decodeAdminPlatformJSON(w, r, &action) {
		return
	}
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		handleKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if action.IdempotencyKey != "" && handleKey != "" && action.IdempotencyKey != handleKey {
			writeError(w, http.StatusConflict, "idempotency_key_conflict", "Header and command idempotency keys must match")
			return
		}
		if handleKey != "" {
			action.IdempotencyKey = handleKey
		}
		h.handleLifecyclePeoplePolicyJob(w, r, tenant, body, action)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
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

func (h *BloemAdminPeopleHandler) HandleCancelPolicyJob(w http.ResponseWriter, r *http.Request) {
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

func (h *BloemAdminPeopleHandler) HandleGetPolicyJob(w http.ResponseWriter, r *http.Request) {
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

func (h *BloemAdminPeopleHandler) HandleGetBulkJob(w http.ResponseWriter, r *http.Request) {
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

func (h *BloemAdminPeopleHandler) HandleUpdateMembership(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireOrganization(w, r)
	if !ok {
		return
	}
	accountID, ok := adminPeoplePathAccount(w, r)
	if !ok {
		return
	}
	body, ok := captureBloemLifecycleBody(w, r)
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
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		h.handleLifecyclePeopleMembership(w, r, tenant, body, accountID, request.ExpectedRevision, request.Status)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
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

func (h *BloemAdminPeopleHandler) HandleUpdateProfile(w http.ResponseWriter, r *http.Request) {
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
	body, ok := captureBloemLifecycleBody(w, r)
	if !ok {
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
	if h.lifecycle != nil && h.lifecycleDigest != nil {
		h.handleLifecyclePeopleProfile(w, r, tenant, body, accountID, profileID, request.ExpectedRevision, request.GroupID)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
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

func peopleLifecycleResult(person adminpeople.PersonSummary) (lifecycleidempotency.Result, error) {
	body, err := json.Marshal(struct {
		Person adminpeople.PersonSummary `json:"person"`
	}{person})
	return lifecycleidempotency.Result{Status: http.StatusOK, Body: body, Headers: map[string][]string{"Content-Type": {"application/json"}}}, err
}

func (h *BloemAdminPeopleHandler) lifecycleService() (bloemAdminPeopleLifecycleService, error) {
	service, ok := h.service.(bloemAdminPeopleLifecycleService)
	if !ok {
		return nil, errors.New("lifecycle-safe people service unavailable")
	}
	return service, nil
}

func (h *BloemAdminPeopleHandler) handleLifecyclePeopleMembership(w http.ResponseWriter, r *http.Request, tenant tenancy.Context, body []byte, accountID int, expectedRevision int64, status tenancy.MembershipStatus) {
	claims, _ := middleware.GetAdminContextClaims(r.Context())
	request, ok := bloemLifecycleRequest(r, claims, h.lifecycleDigest, "people.membership.update", lifecycleidempotency.TargetExactMembership, map[string]string{"organization_id": tenant.OrganizationID.String(), "account_id": strconv.Itoa(accountID)}, body)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Administrative account identity is incomplete")
		return
	}
	request.ResolveTargets = func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
		return resolveBloemAccountMembershipTarget(ctx, tx, tenant.OrganizationID, accountID, "")
	}
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		service, err := h.lifecycleService()
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		person, err := service.UpdateMembershipInTransaction(adminPeopleMutationContext(r.WithContext(ctx)), tx, tenant.OrganizationID, tenant.AccountID, accountID, expectedRevision, status)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		return peopleLifecycleResult(person)
	})
	if err != nil {
		if !writeBloemLifecycleError(w, err) {
			h.writeError(w, r, err, accountID)
		}
		return
	}
	writeBloemLifecycleResult(w, result)
}

func (h *BloemAdminPeopleHandler) handleLifecyclePeopleProfile(w http.ResponseWriter, r *http.Request, tenant tenancy.Context, body []byte, accountID int, profileID string, expectedRevision int64, groupID int) {
	claims, _ := middleware.GetAdminContextClaims(r.Context())
	request, ok := bloemLifecycleRequest(r, claims, h.lifecycleDigest, "people.profile.update", lifecycleidempotency.TargetPathAccount, map[string]string{"organization_id": tenant.OrganizationID.String(), "account_id": strconv.Itoa(accountID), "profile_id": profileID}, body)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Administrative account identity is incomplete")
		return
	}
	request.ResolveTargets = func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
		return resolveBloemAccountMembershipTarget(ctx, tx, tenant.OrganizationID, accountID, profileID)
	}
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		service, err := h.lifecycleService()
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		person, err := service.UpdateProfileGroupInTransaction(adminPeopleMutationContext(r.WithContext(ctx)), tx, tenant.OrganizationID, tenant.AccountID, accountID, profileID, expectedRevision, groupID)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		return peopleLifecycleResult(person)
	})
	if err != nil {
		if !writeBloemLifecycleError(w, err) {
			h.writeError(w, r, err, accountID)
		}
		return
	}
	writeBloemLifecycleResult(w, result)
}

func (h *BloemAdminPeopleHandler) lifecycleSelectionRequest(r *http.Request, tenant tenancy.Context, body []byte, routeID, selectionToken string) (lifecycleidempotency.Request, bool) {
	claims, _ := middleware.GetAdminContextClaims(r.Context())
	request, ok := bloemLifecycleRequest(r, claims, h.lifecycleDigest, routeID, lifecycleidempotency.TargetStoredSelection, map[string]string{"organization_id": tenant.OrganizationID.String()}, body)
	if !ok {
		return lifecycleidempotency.Request{}, false
	}
	request.ResolveTargets = func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, error) {
		resolver, ok := h.service.(bloemAdminPeopleLifecycleSelectionResolver)
		if !ok {
			return nil, errors.New("lifecycle selection resolver unavailable")
		}
		return resolver.ResolveLifecycleSelectionTargets(ctx, tx, tenant.OrganizationID, selectionToken)
	}
	return request, true
}

func bulkLifecycleResult(result adminpeople.BulkResult) (lifecycleidempotency.Result, error) {
	body, err := json.Marshal(struct {
		Job adminpeople.BulkResult `json:"job"`
	}{result})
	return lifecycleidempotency.Result{Status: http.StatusCreated, Body: body, Headers: map[string][]string{"Content-Type": {"application/json"}}, OperationID: result.JobID}, err
}

func (h *BloemAdminPeopleHandler) handleLifecyclePeopleBulkJob(w http.ResponseWriter, r *http.Request, tenant tenancy.Context, body []byte, action adminpeople.BulkAction) {
	request, ok := h.lifecycleSelectionRequest(r, tenant, body, "people.bulk_job.create", action.SelectionToken)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Administrative account identity is incomplete")
		return
	}
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		service, ok := h.service.(bloemAdminPeopleLifecycleJobService)
		if !ok {
			return lifecycleidempotency.Result{}, errors.New("lifecycle people service unavailable")
		}
		queued, err := service.EnqueueBulkInTransaction(adminPeopleMutationContext(r.WithContext(ctx)), tx, tenant.OrganizationID, tenant.AccountID, action)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		return bulkLifecycleResult(queued)
	})
	if err != nil {
		if !writeBloemLifecycleError(w, err) {
			h.writeError(w, r, err, 0)
		}
		return
	}
	if !result.Replayed && h.worker != nil {
		h.worker.Wake()
	}
	writeBloemLifecycleResult(w, result)
}

func (h *BloemAdminPeopleHandler) handleLifecyclePeoplePolicyJob(w http.ResponseWriter, r *http.Request, tenant tenancy.Context, body []byte, action adminpeople.PolicyBulkAction) {
	request, ok := h.lifecycleSelectionRequest(r, tenant, body, "people.policy_job.create", action.SelectionToken)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Administrative account identity is incomplete")
		return
	}
	result, err := h.lifecycle.Execute(r.Context(), request, func(ctx context.Context, tx pgx.Tx, _ lifecycleidempotency.Binding) (lifecycleidempotency.Result, error) {
		service, ok := h.service.(bloemAdminPeopleLifecycleJobService)
		if !ok {
			return lifecycleidempotency.Result{}, errors.New("lifecycle people service unavailable")
		}
		queued, err := service.EnqueuePolicyBulkForScopeInTransaction(adminPeopleMutationContext(r.WithContext(ctx)), tx, tenant.OrganizationID, tenant.AccountID, action, adminpeople.PolicyOperationScopeOrganization)
		if err != nil {
			return lifecycleidempotency.Result{}, err
		}
		return bulkLifecycleResult(queued)
	})
	if err != nil {
		if !writeBloemLifecycleError(w, err) {
			h.writeError(w, r, err, 0)
		}
		return
	}
	if !result.Replayed && h.worker != nil {
		h.worker.Wake()
	}
	writeBloemLifecycleResult(w, result)
}

func (h *BloemAdminPeopleHandler) requireOrganization(w http.ResponseWriter, r *http.Request) (tenancy.Context, bool) {
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

func (h *BloemAdminPeopleHandler) writeError(w http.ResponseWriter, r *http.Request, err error, accountID int) {
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
		writeError(w, http.StatusConflict, "job_not_cancellable", "The requested job cannot be canceled through this endpoint")
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
