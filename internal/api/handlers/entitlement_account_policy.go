package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/google/uuid"
)

const (
	accountPolicySnapshotBodyLimit = 1 << 20
	accountPolicyAccountIDsField   = "account_ids"
)

type accountPolicySnapshotRequest struct {
	AccountIDs []int `json:"account_ids"`
}

type accountPolicyProfileJSON struct {
	ProfileID       string                `json:"profile_id"`
	ProfileName     string                `json:"profile_name"`
	GroupID         int64                 `json:"group_id"`
	InheritsAccount bool                  `json:"inherits_account"`
	State           string                `json:"state"`
	Policy          entitlementPolicyJSON `json:"policy"`
}

type accountPolicySnapshotJSON struct {
	ObservedAt             time.Time                  `json:"observed_at"`
	OrganizationID         uuid.UUID                  `json:"organization_id"`
	AccountID              int                        `json:"account_id"`
	GroupID                int64                      `json:"group_id"`
	CohortID               uuid.UUID                  `json:"cohort_id,omitempty"`
	CohortRevision         int64                      `json:"cohort_revision,omitempty"`
	SourceTemplateKey      string                     `json:"source_template_key,omitempty"`
	SourceTemplateRevision int64                      `json:"source_template_revision,omitempty"`
	State                  string                     `json:"state"`
	PolicyRevision         int64                      `json:"policy_revision"`
	Policy                 entitlementPolicyJSON      `json:"policy"`
	Profiles               []accountPolicyProfileJSON `json:"profiles"`
}

type accountPolicySnapshotResultJSON struct {
	AccountID int                        `json:"account_id"`
	Snapshot  *accountPolicySnapshotJSON `json:"snapshot,omitempty"`
	Error     string                     `json:"error,omitempty"`
}

// HandleGetAccountPolicy returns a direct account's authoritative policy in
// the deployment default organization.
func (h *AdminHandler) HandleGetAccountPolicy(w http.ResponseWriter, r *http.Request) {
	h.handleGetAccountPolicy(w, r, uuid.Nil)
}

// HandleGetOrganizationAccountPolicy returns an account policy only when the
// account belongs to the organization asserted in the path.
func (h *AdminHandler) HandleGetOrganizationAccountPolicy(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := adminPlatformPathUUID(w, r, "organization_id")
	if !ok {
		return
	}
	h.handleGetAccountPolicy(w, r, organizationID)
}

func (h *AdminHandler) handleGetAccountPolicy(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID) {
	if !h.requirePlatformAccountPolicies(w, r) {
		return
	}
	accountID64, ok := v2PositivePathID(w, r, "account_id")
	if !ok || accountID64 > int64(^uint(0)>>1) {
		return
	}
	snapshot, err := h.accountPolicies.GetAccountPolicy(r.Context(), organizationID, int(accountID64))
	if err != nil {
		writeAccountPolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, accountPolicySnapshotToJSON(snapshot))
}

// HandleGetAccountPolicySnapshots returns bounded direct-account policy reads.
func (h *AdminHandler) HandleGetAccountPolicySnapshots(w http.ResponseWriter, r *http.Request) {
	h.handleGetAccountPolicySnapshots(w, r, uuid.Nil)
}

// HandleGetOrganizationAccountPolicySnapshots returns bounded account policy
// reads scoped to one explicit organization.
func (h *AdminHandler) HandleGetOrganizationAccountPolicySnapshots(w http.ResponseWriter, r *http.Request) {
	organizationID, ok := adminPlatformPathUUID(w, r, "organization_id")
	if !ok {
		return
	}
	h.handleGetAccountPolicySnapshots(w, r, organizationID)
}

func (h *AdminHandler) handleGetAccountPolicySnapshots(w http.ResponseWriter, r *http.Request, organizationID uuid.UUID) {
	if !h.requirePlatformAccountPolicies(w, r) {
		return
	}
	var request accountPolicySnapshotRequest
	if !decodeAccountPolicySnapshotRequest(w, r, &request) {
		return
	}
	if len(request.AccountIDs) == 0 || len(request.AccountIDs) > entitlements.MaxAccountPolicySnapshotIDs {
		writeAdminValidation(w, map[string]string{accountPolicyAccountIDsField: "must contain between 1 and 10000 Server account IDs"})
		return
	}
	for _, accountID := range request.AccountIDs {
		if accountID <= 0 {
			writeAdminValidation(w, map[string]string{accountPolicyAccountIDsField: "must contain only positive Server account IDs"})
			return
		}
	}
	items, observedAt, err := h.accountPolicies.GetAccountPolicies(r.Context(), organizationID, request.AccountIDs)
	if err != nil {
		writeAccountPolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ObservedAt time.Time                         `json:"observed_at"`
		Items      []accountPolicySnapshotResultJSON `json:"items"`
	}{ObservedAt: observedAt, Items: accountPolicySnapshotResultsToJSON(items)})
}

func accountPolicySnapshotToJSON(snapshot entitlements.AccountPolicySnapshot) accountPolicySnapshotJSON {
	profiles := make([]accountPolicyProfileJSON, 0, len(snapshot.Profiles))
	for _, profile := range snapshot.Profiles {
		profiles = append(profiles, accountPolicyProfileJSON{
			ProfileID: profile.ProfileID, ProfileName: profile.ProfileName,
			GroupID: profile.GroupID, InheritsAccount: profile.InheritsAccount,
			State: profile.State, Policy: policyToJSON(profile.Policy),
		})
	}
	return accountPolicySnapshotJSON{
		ObservedAt: snapshot.ObservedAt, OrganizationID: snapshot.OrganizationID,
		AccountID: snapshot.AccountID, GroupID: snapshot.GroupID,
		CohortID: snapshot.CohortID, CohortRevision: snapshot.CohortRevision,
		SourceTemplateKey:      snapshot.SourceTemplateKey,
		SourceTemplateRevision: snapshot.SourceTemplateRevision,
		State:                  snapshot.State, PolicyRevision: snapshot.PolicyRevision,
		Policy: policyToJSON(snapshot.Policy), Profiles: profiles,
	}
}

func accountPolicySnapshotResultsToJSON(items []entitlements.AccountPolicySnapshotResult) []accountPolicySnapshotResultJSON {
	result := make([]accountPolicySnapshotResultJSON, 0, len(items))
	for _, item := range items {
		converted := accountPolicySnapshotResultJSON{AccountID: item.AccountID, Error: item.Error}
		if item.Snapshot != nil {
			snapshot := accountPolicySnapshotToJSON(*item.Snapshot)
			converted.Snapshot = &snapshot
		}
		result = append(result, converted)
	}
	return result
}

func (h *AdminHandler) requirePlatformAccountPolicies(w http.ResponseWriter, r *http.Request) bool {
	claims, ok := middleware.GetAdminContextClaims(r.Context())
	if !ok || claims.Scope != auth.AdminScopePlatform || claims.AccountID <= 0 {
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
		return false
	}
	if h == nil || h.accountPolicies == nil {
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Account entitlement policies are unavailable")
		return false
	}
	return true
}

func decodeAccountPolicySnapshotRequest(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, accountPolicySnapshotBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid entitlement snapshot request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid entitlement snapshot request")
		return false
	}
	return true
}

func writeAccountPolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, entitlements.ErrAccountNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Entitlement resource not found")
	case errors.Is(err, entitlements.ErrAccountPolicySnapshotLimit), errors.Is(err, entitlements.ErrInvalidAccountPolicyIDs):
		writeAdminValidation(w, map[string]string{accountPolicyAccountIDsField: "must contain between 1 and 10000 positive Server account IDs"})
	default:
		writeError(w, http.StatusServiceUnavailable, "entitlements_unavailable", "Account entitlement policies are unavailable")
	}
}
