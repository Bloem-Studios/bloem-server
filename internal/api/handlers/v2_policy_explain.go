package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type adminOrganizationRef struct {
	ID           uuid.UUID `json:"id"`
	MembershipID uuid.UUID `json:"membership_id,omitempty"`
}

type adminSubjectRef struct {
	AccountID int    `json:"account_id,omitempty"`
	ProfileID string `json:"profile_id,omitempty"`
}

type adminGroupRef struct {
	ID   int64  `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type PolicyVersionRef struct {
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	Version int64  `json:"version"`
}

type PolicyDecisionExplanation struct {
	ID             int64                `json:"id"`
	Timestamp      time.Time            `json:"timestamp"`
	Organization   adminOrganizationRef `json:"organization"`
	Subject        adminSubjectRef      `json:"subject"`
	Group          adminGroupRef        `json:"group"`
	LibraryCeiling []int                `json:"library_ceiling"`
	Action         string               `json:"action"`
	Resource       map[string]any       `json:"resource"`
	Allowed        bool                 `json:"allowed"`
	ReasonCode     string               `json:"reason_code"`
	PolicyVersions []PolicyVersionRef   `json:"policy_versions"`
}

type V2OrganizationDecisionStore interface {
	ListForOrganization(context.Context, uuid.UUID, policy.ListOptions) (policy.ListResult, error)
	GetForOrganization(context.Context, uuid.UUID, int64, *time.Time) (policy.Entry, error)
}

type V2PolicyExplainHandler struct{ decisions V2OrganizationDecisionStore }

func NewV2PolicyExplainHandler(decisions V2OrganizationDecisionStore) *V2PolicyExplainHandler {
	return &V2PolicyExplainHandler{decisions: decisions}
}

func (h *V2PolicyExplainHandler) HandleListDecisions(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.require(w, r)
	if !ok {
		return
	}
	options := policy.ListOptions{Cursor: strings.TrimSpace(r.URL.Query().Get("cursor")), DecisionName: strings.TrimSpace(r.URL.Query().Get("decision_name"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > 200 {
			writeAdminValidation(w, map[string]string{"limit": "must be between 1 and 200"})
			return
		}
		options.Limit = limit
	}
	result, err := h.decisions.ListForOrganization(r.Context(), tenant.OrganizationID, options)
	if err != nil {
		writeV2PolicyDecisionError(w, err)
		return
	}
	items := make([]PolicyDecisionExplanation, 0, len(result.Entries))
	for _, entry := range result.Entries {
		items = append(items, explainPolicyDecision(entry))
	}
	writeJSON(w, http.StatusOK, struct {
		Decisions  []PolicyDecisionExplanation `json:"decisions"`
		NextCursor string                      `json:"next_cursor,omitempty"`
	}{items, result.NextCursor})
}

func (h *V2PolicyExplainHandler) HandleGetDecision(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.require(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	entry, err := h.decisions.GetForOrganization(r.Context(), tenant.OrganizationID, id, nil)
	if err != nil {
		writeV2PolicyDecisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Decision PolicyDecisionExplanation `json:"decision"`
	}{explainPolicyDecision(entry)})
}

func (h *V2PolicyExplainHandler) require(w http.ResponseWriter, r *http.Request) (tenancy.Context, bool) {
	tenant, ok := requireV2OrganizationContext(w, r)
	if !ok {
		return tenancy.Context{}, false
	}
	if h == nil || h.decisions == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Policy decisions are unavailable")
		return tenancy.Context{}, false
	}
	return tenant, true
}

func explainPolicyDecision(entry policy.Entry) PolicyDecisionExplanation {
	input := decodeDecisionObject(entry.InputSample)
	result := decodeDecisionObject(entry.ResultSample)
	explanation := PolicyDecisionExplanation{
		ID: entry.ID, Timestamp: entry.Timestamp,
		Organization: adminOrganizationRef{ID: entry.OrganizationID, MembershipID: entry.MembershipID},
		Subject:      adminSubjectRef{ProfileID: entry.ProfileID}, Resource: map[string]any{},
		PolicyVersions: []PolicyVersionRef{{Kind: "vendor", Version: entry.PolicyGeneration}},
	}
	if entry.UserID != nil {
		explanation.Subject.AccountID = *entry.UserID
	}
	if value, ok := integerValue(input["user_id"]); ok {
		explanation.Subject.AccountID = int(value)
	}
	if value, ok := input["profile_id"].(string); ok && value != "" {
		explanation.Subject.ProfileID = value
	}
	if value, ok := integerValue(input["access_group_id"]); ok {
		explanation.Group.ID = value
	}
	if value, ok := input["access_group_name"].(string); ok {
		explanation.Group.Name = value
	}
	explanation.LibraryCeiling = integerSlice(input["tenant_library_ids"])
	if len(explanation.LibraryCeiling) == 0 {
		explanation.LibraryCeiling = integerSlice(input["library_ceiling"])
	}
	if value, ok := input["action"].(string); ok {
		explanation.Action = value
	}
	if explanation.Action == "" {
		explanation.Action = string(entry.DecisionName)
	}
	if resource, ok := input["resource"].(map[string]any); ok {
		explanation.Resource = redactDecisionMap(resource)
	}
	for key := range input {
		if sensitiveDecisionKey(key) {
			explanation.Resource[key] = "[redacted]"
		}
	}
	if entry.Allowed != nil {
		explanation.Allowed = *entry.Allowed
	}
	if value, ok := result["allowed"].(bool); ok {
		explanation.Allowed = value
	}
	if value, ok := result["reason_code"].(string); ok {
		explanation.ReasonCode = value
	}
	if versions, ok := input["policy_versions"].([]any); ok {
		for _, raw := range versions {
			value, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			version, valid := integerValue(value["version"])
			if !valid {
				continue
			}
			kind, _ := value["kind"].(string)
			name, _ := value["name"].(string)
			if kind == "custom" {
				explanation.PolicyVersions = append(explanation.PolicyVersions, PolicyVersionRef{Kind: kind, Name: name, Version: version})
			}
		}
	}
	return explanation
}

func decodeDecisionObject(raw json.RawMessage) map[string]any {
	value := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

func redactDecisionMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		if sensitiveDecisionKey(key) {
			result[key] = "[redacted]"
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			result[key] = redactDecisionMap(typed)
		case []any:
			items := make([]any, len(typed))
			for i, item := range typed {
				if nested, ok := item.(map[string]any); ok {
					items[i] = redactDecisionMap(nested)
				} else {
					items[i] = item
				}
			}
			result[key] = items
		default:
			result[key] = value
		}
	}
	return result
}

func sensitiveDecisionKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, fragment := range []string{"password", "secret", "token", "credential", "api_key", "authorization", "cookie", "client_ip", "device_id", "session_id"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func integerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), typed == float64(int64(typed))
	case json.Number:
		value, err := typed.Int64()
		return value, err == nil
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	}
	return 0, false
}
func integerSlice(value any) []int {
	raw, ok := value.([]any)
	if !ok {
		return []int{}
	}
	result := make([]int, 0, len(raw))
	for _, item := range raw {
		if number, valid := integerValue(item); valid {
			result = append(result, int(number))
		}
	}
	return result
}

func writeV2PolicyDecisionError(w http.ResponseWriter, err error) {
	if errors.Is(err, policy.ErrDecisionNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Policy decisions are unavailable")
}
