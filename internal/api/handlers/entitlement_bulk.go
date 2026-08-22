package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	platformEntitlementBulkBodyLimit = 1 << 20
	platformEntitlementBulkMaxIDs    = 10000
	platformEntitlementBulkFalse     = "false"
	platformEntitlementBulkTrue      = "true"
	platformEntitlementBulkRequest   = "request"
	entitlementIncludeArchivedParam  = "include_archived"
	entitlementQueryValidationField  = "query"
)

var ErrPlatformEntitlementBulkRateLimited = errors.New("platform entitlement bulk request rate limited")

type PlatformEntitlementBulkCohortStore interface {
	ListCohorts(context.Context, uuid.UUID, bool) ([]entitlements.CohortRevision, error)
	GetCohort(context.Context, uuid.UUID, uuid.UUID) (entitlements.CohortRevision, error)
}

type PlatformEntitlementBulkPeopleService interface {
	CreateSelection(context.Context, uuid.UUID, adminpeople.Filter) (adminpeople.Selection, error)
	PreviewPolicyForScope(context.Context, uuid.UUID, int, string, adminpeople.PolicyCommand, adminpeople.PolicyOperationScope) (adminpeople.PolicyPreview, error)
	EnqueuePolicyBulkForScope(context.Context, uuid.UUID, int, adminpeople.PolicyBulkAction, adminpeople.PolicyOperationScope) (adminpeople.BulkResult, error)
	GetPolicyBulkJob(context.Context, uuid.UUID, string) (adminpeople.BulkResult, error)
	CancelPolicyBulkJob(context.Context, uuid.UUID, int, string) (adminpeople.BulkResult, error)
}

type PlatformEntitlementBulkOrganizationStore interface {
	DefaultOrganization(context.Context) (tenancy.Organization, error)
	GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error)
}

type platformEntitlementBulkPreviewRequest struct {
	AccountIDs []int                     `json:"account_ids"`
	Command    adminpeople.PolicyCommand `json:"command"`
}

type platformEntitlementCohort struct {
	CohortID               uuid.UUID              `json:"cohort_id"`
	OrganizationID         uuid.UUID              `json:"organization_id"`
	Name                   string                 `json:"name"`
	Revision               int64                  `json:"revision"`
	AccessGroupID          int64                  `json:"access_group_id"`
	SourceTemplateKey      string                 `json:"source_template_key,omitempty"`
	SourceTemplateRevision int64                  `json:"source_template_revision,omitempty"`
	ParentCohortID         uuid.UUID              `json:"parent_cohort_id,omitempty"`
	DerivationKind         string                 `json:"derivation_kind"`
	Policy                 adminpeople.PolicyView `json:"policy"`
	PolicyDigest           string                 `json:"policy_digest"`
	Archived               bool                   `json:"archived"`
	MemberCount            int64                  `json:"member_count"`
	CreatedByAccountID     int                    `json:"created_by_account_id,omitempty"`
	CreatedAt              time.Time              `json:"created_at"`
}

func (h *AdminHandler) HandleListPlatformEntitlementCohorts(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requirePlatformEntitlementBulk(w, r); !ok {
		return
	}
	organizationID, ok := h.platformEntitlementOrganization(w, r, false)
	if !ok {
		return
	}
	includeArchived := false
	query := r.URL.Query()
	for key := range query {
		if key != entitlementIncludeArchivedParam {
			writeAdminValidation(w, map[string]string{entitlementQueryValidationField: "contains unsupported parameters"})
			return
		}
	}
	if raw := strings.TrimSpace(query.Get(entitlementIncludeArchivedParam)); raw != "" {
		if raw != platformEntitlementBulkTrue && raw != platformEntitlementBulkFalse {
			writeAdminValidation(w, map[string]string{entitlementIncludeArchivedParam: "must be true or false"})
			return
		}
		includeArchived = raw == platformEntitlementBulkTrue
	}
	items, err := h.platformEntitlementCohorts.ListCohorts(r.Context(), organizationID, includeArchived)
	if err != nil {
		h.writePlatformEntitlementBulkError(w, err)
		return
	}
	cohorts := make([]platformEntitlementCohort, 0, len(items))
	for _, item := range items {
		if item.OrganizationID != organizationID {
			h.writePlatformEntitlementBulkError(w, errors.New("cohort store returned a foreign organization"))
			return
		}
		cohorts = append(cohorts, platformEntitlementCohortFromDomain(item))
	}
	writeJSON(w, http.StatusOK, struct {
		Cohorts []platformEntitlementCohort `json:"cohorts"`
	}{Cohorts: cohorts})
}

func (h *AdminHandler) HandleGetPlatformEntitlementCohort(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requirePlatformEntitlementBulk(w, r); !ok {
		return
	}
	organizationID, ok := h.platformEntitlementOrganization(w, r, false)
	if !ok {
		return
	}
	cohortID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "cohort_id")))
	if err != nil || cohortID == uuid.Nil {
		writeError(w, http.StatusNotFound, "not_found", "Entitlement resource not found")
		return
	}
	item, err := h.platformEntitlementCohorts.GetCohort(r.Context(), organizationID, cohortID)
	if err != nil || item.ID != cohortID || item.OrganizationID != organizationID {
		if err == nil {
			err = entitlements.ErrCohortNotFound
		}
		h.writePlatformEntitlementBulkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Cohort platformEntitlementCohort `json:"cohort"`
	}{Cohort: platformEntitlementCohortFromDomain(item)})
}

func (h *AdminHandler) HandleCreatePlatformOrganizationPolicyPreview(w http.ResponseWriter, r *http.Request) {
	h.handleCreatePlatformPolicyPreview(w, r, false)
}

func (h *AdminHandler) HandleCreatePlatformDirectPolicyPreview(w http.ResponseWriter, r *http.Request) {
	h.handleCreatePlatformPolicyPreview(w, r, true)
}

func (h *AdminHandler) handleCreatePlatformPolicyPreview(w http.ResponseWriter, r *http.Request, direct bool) {
	actorID, ok := h.requirePlatformEntitlementBulk(w, r)
	if !ok {
		return
	}
	organizationID, ok := h.platformEntitlementOrganization(w, r, direct)
	if !ok {
		return
	}
	var request platformEntitlementBulkPreviewRequest
	if !decodePlatformEntitlementBulkJSON(w, r, &request) {
		return
	}
	accountIDs, ok := validatePlatformEntitlementBulkIDs(w, request.AccountIDs)
	if !ok {
		return
	}
	ctx := platformEntitlementBulkMutationContext(r, actorID)
	selection, err := h.platformEntitlementPeople.CreateSelection(ctx, organizationID, adminpeople.Filter{
		AccountIDs: accountIDs, RequireAllAccountIDs: true,
		Status: []tenancy.MembershipStatus{tenancy.MembershipActive},
	})
	if err != nil {
		h.writePlatformEntitlementBulkError(w, err)
		return
	}
	if selection.Matched != int64(len(accountIDs)) {
		h.writePlatformEntitlementBulkError(w, adminpeople.ErrNotFound)
		return
	}
	preview, err := h.platformEntitlementPeople.PreviewPolicyForScope(ctx, organizationID, actorID, selection.Token, request.Command, platformEntitlementOperationScope(direct))
	if err != nil {
		h.writePlatformEntitlementBulkError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Selection adminpeople.Selection     `json:"selection"`
		Preview   adminpeople.PolicyPreview `json:"preview"`
	}{Selection: selection, Preview: preview})
}

func (h *AdminHandler) HandleCreatePlatformOrganizationPolicyJob(w http.ResponseWriter, r *http.Request) {
	h.handleCreatePlatformPolicyJob(w, r, false)
}

func (h *AdminHandler) HandleCreatePlatformDirectPolicyJob(w http.ResponseWriter, r *http.Request) {
	h.handleCreatePlatformPolicyJob(w, r, true)
}

func (h *AdminHandler) handleCreatePlatformPolicyJob(w http.ResponseWriter, r *http.Request, direct bool) {
	actorID, ok := h.requirePlatformEntitlementBulk(w, r)
	if !ok {
		return
	}
	organizationID, ok := h.platformEntitlementOrganization(w, r, direct)
	if !ok {
		return
	}
	var action adminpeople.PolicyBulkAction
	if !decodePlatformEntitlementBulkJSON(w, r, &action) {
		return
	}
	result, err := h.platformEntitlementPeople.EnqueuePolicyBulkForScope(platformEntitlementBulkMutationContext(r, actorID), organizationID, actorID, action, platformEntitlementOperationScope(direct))
	if err != nil {
		h.writePlatformEntitlementBulkError(w, err)
		return
	}
	if h.platformEntitlementWorker != nil {
		h.platformEntitlementWorker.Wake()
	}
	writeJSON(w, http.StatusCreated, struct {
		Job adminpeople.BulkResult `json:"job"`
	}{Job: result})
}

func platformEntitlementOperationScope(direct bool) adminpeople.PolicyOperationScope {
	if direct {
		return adminpeople.PolicyOperationScopeDirectAccounts
	}
	return adminpeople.PolicyOperationScopeOrganization
}

func (h *AdminHandler) HandleGetPlatformOrganizationPolicyJob(w http.ResponseWriter, r *http.Request) {
	h.handleGetPlatformPolicyJob(w, r, false)
}

func (h *AdminHandler) HandleGetPlatformDirectPolicyJob(w http.ResponseWriter, r *http.Request) {
	h.handleGetPlatformPolicyJob(w, r, true)
}

func (h *AdminHandler) handleGetPlatformPolicyJob(w http.ResponseWriter, r *http.Request, direct bool) {
	if _, ok := h.requirePlatformEntitlementBulk(w, r); !ok {
		return
	}
	organizationID, ok := h.platformEntitlementOrganization(w, r, direct)
	if !ok {
		return
	}
	jobID := strings.TrimSpace(chi.URLParam(r, "job_id"))
	if jobID == "" || len(jobID) > 128 {
		writeError(w, http.StatusNotFound, "not_found", "Entitlement resource not found")
		return
	}
	result, err := h.platformEntitlementPeople.GetPolicyBulkJob(r.Context(), organizationID, jobID)
	if err != nil {
		h.writePlatformEntitlementBulkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Job adminpeople.BulkResult `json:"job"`
	}{Job: result})
}

func (h *AdminHandler) HandleCancelPlatformOrganizationPolicyJob(w http.ResponseWriter, r *http.Request) {
	h.handleCancelPlatformPolicyJob(w, r, false)
}

func (h *AdminHandler) HandleCancelPlatformDirectPolicyJob(w http.ResponseWriter, r *http.Request) {
	h.handleCancelPlatformPolicyJob(w, r, true)
}

func (h *AdminHandler) handleCancelPlatformPolicyJob(w http.ResponseWriter, r *http.Request, direct bool) {
	actorID, ok := h.requirePlatformEntitlementBulk(w, r)
	if !ok {
		return
	}
	organizationID, ok := h.platformEntitlementOrganization(w, r, direct)
	if !ok {
		return
	}
	var request struct{}
	if !decodePlatformEntitlementBulkJSON(w, r, &request) {
		return
	}
	jobID := strings.TrimSpace(chi.URLParam(r, "job_id"))
	if jobID == "" || len(jobID) > 128 {
		writeError(w, http.StatusNotFound, "not_found", "Entitlement resource not found")
		return
	}
	result, err := h.platformEntitlementPeople.CancelPolicyBulkJob(platformEntitlementBulkMutationContext(r, actorID), organizationID, actorID, jobID)
	if err != nil {
		h.writePlatformEntitlementBulkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Job adminpeople.BulkResult `json:"job"`
	}{Job: result})
}

func (h *AdminHandler) requirePlatformEntitlementBulk(w http.ResponseWriter, r *http.Request) (int, bool) {
	if h == nil || h.platformEntitlementCohorts == nil || h.platformEntitlementPeople == nil || h.platformEntitlementOrganizations == nil {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Bulk entitlement policies are unavailable")
		return 0, false
	}
	return h.requirePlatformEntitlementActor(w, r, "Bulk entitlement policies are unavailable")
}

func (h *AdminHandler) requirePlatformEntitlementActor(w http.ResponseWriter, r *http.Request, unavailableMessage string) (int, bool) {
	if claims, ok := middleware.GetAdminContextClaims(r.Context()); ok && claims.Scope == auth.AdminScopePlatform && claims.AccountID > 0 {
		return claims.AccountID, true
	}
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.TokenType != auth.TokenTypeAPIKey || claims.UserID <= 0 || claims.Role != models.RoleAdmin ||
		(len(claims.APIKeyScopes) > 0 && !slices.Contains(claims.APIKeyScopes, auth.ScopeAdminEntitlementsBulk)) {
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
		return 0, false
	}
	if h == nil || h.platformEntitlementAuthorizer == nil {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", unavailableMessage)
		return 0, false
	}
	allowed, err := h.platformEntitlementAuthorizer.IsPlatformAdmin(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", unavailableMessage)
		return 0, false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
		return 0, false
	}
	return claims.UserID, true
}

func (h *AdminHandler) platformEntitlementOrganization(w http.ResponseWriter, r *http.Request, direct bool) (uuid.UUID, bool) {
	var organization tenancy.Organization
	var err error
	if direct {
		organization, err = h.platformEntitlementOrganizations.DefaultOrganization(r.Context())
		if err == nil && !organization.Default {
			err = tenancy.ErrOrganizationNotFound
		}
	} else {
		organizationID, parseErr := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "organization_id")))
		if parseErr != nil || organizationID == uuid.Nil {
			writeError(w, http.StatusNotFound, "not_found", "Entitlement resource not found")
			return uuid.Nil, false
		}
		organization, err = h.platformEntitlementOrganizations.GetOrganization(r.Context(), organizationID)
		if err == nil && organization.ID != organizationID {
			err = tenancy.ErrOrganizationNotFound
		}
	}
	if err != nil || organization.ID == uuid.Nil || organization.Status != tenancy.OrganizationActive {
		if err == nil {
			err = tenancy.ErrOrganizationNotFound
		}
		h.writePlatformEntitlementBulkError(w, err)
		return uuid.Nil, false
	}
	return organization.ID, true
}

func platformEntitlementBulkMutationContext(r *http.Request, actorID int) context.Context {
	return adminpeople.WithMutationActor(r.Context(), adminpeople.MutationActor{
		AccountID: actorID, Authority: adminpeople.AuthorityPlatformAdmin, RequestID: adminRequestID(r),
	})
}

func validatePlatformEntitlementBulkIDs(w http.ResponseWriter, input []int) ([]int, bool) {
	if len(input) == 0 || len(input) > platformEntitlementBulkMaxIDs {
		writeAdminValidation(w, map[string]string{accountPolicyAccountIDsField: "must contain between 1 and 10000 Server account IDs"})
		return nil, false
	}
	seen := make(map[int]struct{}, len(input))
	accountIDs := make([]int, 0, len(input))
	for _, accountID := range input {
		if accountID <= 0 {
			writeAdminValidation(w, map[string]string{accountPolicyAccountIDsField: "must contain only positive Server account IDs"})
			return nil, false
		}
		if _, exists := seen[accountID]; exists {
			writeAdminValidation(w, map[string]string{accountPolicyAccountIDsField: "must not contain duplicate Server account IDs"})
			return nil, false
		}
		seen[accountID] = struct{}{}
		accountIDs = append(accountIDs, accountID)
	}
	slices.Sort(accountIDs)
	return accountIDs, true
}

func decodePlatformEntitlementBulkJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, platformEntitlementBulkBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid bulk entitlement request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid bulk entitlement request")
		return false
	}
	return true
}

func (h *AdminHandler) writePlatformEntitlementBulkError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, entitlements.ErrCohortNotFound), errors.Is(err, adminpeople.ErrNotFound), errors.Is(err, adminpeople.ErrInvalidSelection), errors.Is(err, tenancy.ErrOrganizationNotFound), errors.Is(err, tenancy.ErrOwnershipResolutionRequired):
		writeError(w, http.StatusNotFound, "not_found", "Entitlement resource not found")
	case errors.Is(err, adminpeople.ErrSelectionExpired):
		writeError(w, http.StatusConflict, "selection_expired", "The immutable selection expired; create a new preview")
	case errors.Is(err, adminpeople.ErrInvalidPolicyConfirmation):
		writeError(w, http.StatusConflict, "policy_confirmation_stale", "The policy preview changed or expired; create a new preview")
	case errors.Is(err, adminpeople.ErrBulkIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", "The idempotency key was used for a different command")
	case errors.Is(err, adminpeople.ErrBulkJobNotCancellable):
		writeError(w, http.StatusConflict, "job_not_cancellable", "The requested policy job cannot be canceled")
	case errors.Is(err, adminpeople.ErrAuthorizationStateChanged):
		writeError(w, http.StatusConflict, "authorization_state_changed", "Authorization state changed; create a new preview")
	case errors.Is(err, adminpeople.ErrInvalidFilter), errors.Is(err, adminpeople.ErrInvalidPolicyCommand):
		writeAdminValidation(w, map[string]string{platformEntitlementBulkRequest: "contains an invalid entitlement policy operation"})
	case errors.Is(err, ErrPlatformEntitlementBulkRateLimited):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many bulk entitlement requests")
	default:
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Bulk entitlement policies are unavailable")
	}
}

func platformEntitlementCohortFromDomain(item entitlements.CohortRevision) platformEntitlementCohort {
	return platformEntitlementCohort{
		CohortID: item.ID, OrganizationID: item.OrganizationID, Name: item.Name, Revision: item.Revision,
		AccessGroupID: item.AccessGroupID, SourceTemplateKey: item.SourceTemplateKey,
		SourceTemplateRevision: item.SourceTemplateRevision, ParentCohortID: item.ParentID,
		DerivationKind: item.DerivationKind, Policy: adminpeople.PolicyView{
			LibraryIDs: item.Policy.LibraryIDs, PlaybackAllowed: item.Policy.PlaybackAllowed,
			MaxStreams: item.Policy.MaxStreams, MaxProfiles: item.Policy.MaxProfiles,
			TranscodeAllowed: item.Policy.TranscodeAllowed, MaxTranscodes: item.Policy.MaxTranscodes,
			DownloadAllowed: item.Policy.DownloadAllowed, DownloadTranscodeAllowed: item.Policy.DownloadTranscodeAllowed,
			MaxPlaybackQuality: item.Policy.MaxPlaybackQuality, AllowedPermissions: item.Policy.AllowedPermissions,
			RequestsAllowed: item.Policy.RequestsAllowed,
		}, PolicyDigest: item.PolicyDigest, Archived: item.Archived, MemberCount: item.MemberCount,
		CreatedByAccountID: item.CreatedByAccountID, CreatedAt: item.CreatedAt,
	}
}
