package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/google/uuid"
)

type organizationDecisionStub struct {
	gotOrganization uuid.UUID
	gotID           int64
	result          policy.ListResult
	entry           policy.Entry
	err             error
}

func (s *organizationDecisionStub) ListForOrganization(_ context.Context, id uuid.UUID, options policy.ListOptions) (policy.ListResult, error) {
	s.gotOrganization = id
	return s.result, s.err
}
func (s *organizationDecisionStub) GetForOrganization(_ context.Context, id uuid.UUID, decisionID int64, _ *time.Time) (policy.Entry, error) {
	s.gotOrganization, s.gotID = id, decisionID
	return s.entry, s.err
}

func TestV2PolicyExplainReturnsStructuredRedactedOrganizationExplanation(t *testing.T) {
	organizationID := uuid.New()
	membershipID := uuid.New()
	allowed := false
	input := map[string]any{
		"tenant":  map[string]any{"organization_id": organizationID.String(), "membership_id": membershipID.String()},
		"user_id": 17, "profile_id": "profile-7", "access_group_id": 4,
		"tenant_library_ids": []any{2.0, 7.0}, "action": "download", "resource": map[string]any{"folder_id": 7.0, "title": "Visible", "access_token": "secret-token"},
		"policy_versions": []any{map[string]any{"kind": "custom", "name": "downloads", "version": 5.0}},
		"password":        "hunter2", "client_ip": "203.0.113.9", "device_id": "private-device",
	}
	inputJSON, _ := json.Marshal(input)
	resultJSON := json.RawMessage(`{"allowed":false,"reason_code":"downloads_disabled"}`)
	store := &organizationDecisionStub{entry: policy.Entry{ID: 41, OrganizationID: organizationID, MembershipID: membershipID, DecisionName: policy.DecisionAction, UserID: intPointer(17), ProfileID: "profile-7", Allowed: &allowed, PolicyGeneration: 13, InputSample: inputJSON, ResultSample: resultJSON}}
	h := NewV2PolicyExplainHandler(store)
	rec := httptest.NewRecorder()
	h.HandleGetDecision(rec, organizationRequest(http.MethodGet, "/policy-decisions/41", "", organizationID, 7, map[string]string{"id": "41"}))

	body := rec.Body.String()
	for _, required := range []string{`"organization"`, organizationID.String(), `"membership_id":"` + membershipID.String(), `"account_id":17`, `"profile_id":"profile-7"`, `"group"`, `"id":4`, `"library_ceiling":[2,7]`, `"action":"download"`, `"folder_id":7`, `"allowed":false`, `"reason_code":"downloads_disabled"`, `"kind":"vendor"`, `"version":13`, `"kind":"custom"`, `"name":"downloads"`, `"version":5`, `"access_token":"[redacted]"`, `"password":"[redacted]"`} {
		if !strings.Contains(body, required) {
			t.Fatalf("response missing %s: %d %s", required, rec.Code, body)
		}
	}
	for _, forbidden := range []string{"secret-token", "hunter2", "203.0.113.9", "private-device", `"input_sample"`, `"result_sample"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
}

func TestV2PolicyExplainForeignDecisionIsNonDisclosing404(t *testing.T) {
	organizationID := uuid.New()
	store := &organizationDecisionStub{err: policy.ErrDecisionNotFound}
	h := NewV2PolicyExplainHandler(store)
	rec := httptest.NewRecorder()
	h.HandleGetDecision(rec, organizationRequest(http.MethodGet, "/policy-decisions/99", "", organizationID, 7, map[string]string{"id": "99"}))
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "99") || store.gotOrganization != organizationID {
		t.Fatalf("response=%d %s target=%s/%d", rec.Code, rec.Body.String(), store.gotOrganization, store.gotID)
	}
}

func TestV2PolicyExplainDoesNotExposePolicyDocumentMutations(t *testing.T) {
	organizationID := uuid.New()
	store := &organizationDecisionStub{result: policy.ListResult{Entries: []policy.Entry{}}}
	h := NewV2PolicyExplainHandler(store)
	rec := httptest.NewRecorder()
	h.HandleListDecisions(rec, organizationRequest(http.MethodGet, "/policy-decisions", "", organizationID, 7, nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "rego_source") {
		t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
	}
}

func intPointer(value int) *int { return &value }
