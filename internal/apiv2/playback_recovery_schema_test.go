package apiv2

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Validate the emitted operation schemas, including $ref siblings and standard
// if/then constraints. Huma's fast validator does not implement those keywords.
func TestPlaybackRecoveryConditionalSchemas(t *testing.T) {
	doc := generatedDocument(t)
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	const origin = "https://schema.example.invalid/playback-recovery.json"
	if err := compiler.AddResource(origin, doc); err != nil {
		t.Fatal(err)
	}
	type response struct {
		name, path, method, status, fixture string
		recovery                            string
	}
	routes := []response{
		{"start201", "/api/v2/playback/start", "post", "201", "playback_start_opaque_ids", "start"},
		{"start202", "/api/v2/playback/start", "post", "202", "", "pending"},
		{"stop200", "/api/v2/playback/{session_id}", "delete", "200", "playback_stop_completed", "stop"},
		{"stop202", "/api/v2/playback/{session_id}", "delete", "202", "playback_stop_draining", "pending"},
		{"progress", "/api/v2/playback/{session_id}/progress", "post", "200", "playback_progress_applied", ""},
		{"replan", "/api/v2/playback/{session_id}/replan", "post", "200", "playback_start_opaque_ids", ""},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			pointer := "#/paths/" + strings.ReplaceAll(route.path, "/", "~1") + "/" + route.method + "/responses/" + route.status + "/content/application~1json/schema"
			schema, err := compiler.Compile(origin + pointer)
			if err != nil {
				t.Fatal(err)
			}
			valid := func(name string, body map[string]any, want bool) {
				t.Helper()
				t.Run(name, func(t *testing.T) {
					if err := schema.Validate(body); (err == nil) != want {
						t.Fatalf("valid=%v: %v", want, err)
					}
				})
			}
			if route.fixture != "" {
				data, err := os.ReadFile("../../contracts/api/v2/fixtures/" + route.fixture + ".json")
				if err != nil {
					t.Fatal(err)
				}
				var original map[string]any
				if err := json.Unmarshal(data, &original); err != nil {
					t.Fatal(err)
				}
				valid("original-fixture", original, true)
				// The old outcome field was intentionally loose. Absence of recovery must
				// not silently strengthen any ordinary response requirement or enum.
				if route.method == "delete" || route.name == "progress" {
					valid("ordinary-loose-outcome", map[string]any{"outcome": "future_ordinary_outcome"}, true)
				} else {
					valid("ordinary-terminal", map[string]any{"protocol_version": float64(3), "server_features": []any{}, "outcome": "adaptation_unavailable", "terminal": map[string]any{"reason": "client_timeline_changed", "message": "Changed", "retryable": false}}, true)
				}
				original["recovery"] = recoverySchemaBody("pending")["recovery"]
				valid("mixed-ordinary-recovery", original, false)
			}
			for _, kind := range []string{"start", "stop", "pending"} {
				valid(kind, recoverySchemaBody(kind), route.recovery == kind && route.recovery != "")
			}
			if route.recovery == "" {
				return
			}
			body := recoverySchemaBody(route.recovery)
			recovery := body["recovery"].(map[string]any)
			for _, field := range []string{"recovery_id", "playback_attempt_id", "session_id", "state", "reason"} {
				saved := recovery[field]
				delete(recovery, field)
				valid("missing-"+field, cloneRecoverySchemaBody(t, body), false)
				recovery[field] = saved
			}
			for field, value := range map[string]any{"recovery_id": "invalid", "session_id": "invalid", "state": "unknown", "reason": "unknown", "extra": "unknown"} {
				saved, exists := recovery[field]
				recovery[field] = value
				valid("invalid-"+field, cloneRecoverySchemaBody(t, body), false)
				if exists {
					recovery[field] = saved
				} else {
					delete(recovery, field)
				}
			}
			savedOutcome := body["outcome"]
			body["outcome"] = "playable"
			valid("playable-recovery", cloneRecoverySchemaBody(t, body), false)
			body["outcome"] = savedOutcome
			accepted := map[string]any{"sequence": float64(1), "position": float64(0), "is_paused": false}
			recovery["accepted"] = accepted
			valid("accepted", cloneRecoverySchemaBody(t, body), route.recovery != "pending")
			if route.recovery != "pending" {
				for field, value := range map[string]any{"sequence": float64(0), "position": float64(-1), "is_paused": "false", "unknown": true} {
					saved, exists := accepted[field]
					accepted[field] = value
					valid("invalid-accepted-"+field, cloneRecoverySchemaBody(t, body), false)
					if exists {
						accepted[field] = saved
					} else {
						delete(accepted, field)
					}
				}
				accepted["timeline_id"] = strings.Repeat("a", 64)
				accepted["item_position"] = float64(48)
				valid("bound-accepted", cloneRecoverySchemaBody(t, body), true)
			}
			delete(recovery, "accepted")
			for field, value := range map[string]any{"session_id": "11111111-1111-4111-8111-111111111111", "playback_plan": map[string]any{}, "progress_timeline": map[string]any{}, "stop_id": playbackTestStop, "accepted": accepted, "history_id": "history", "unknown": true} {
				body[field] = value
				valid("mixed-"+field, cloneRecoverySchemaBody(t, body), false)
				delete(body, field)
			}
			if route.recovery == "start" {
				terminal := body["terminal"].(map[string]any)
				terminal["retryable"] = true
				valid("retryable-terminal", cloneRecoverySchemaBody(t, body), false)
				terminal["retryable"] = false
				terminal["reason"] = "unknown"
				valid("wrong-terminal-reason", cloneRecoverySchemaBody(t, body), false)
			} else {
				body["terminal"] = map[string]any{}
				valid("unexpected-terminal", cloneRecoverySchemaBody(t, body), false)
				delete(body, "terminal")
			}
			body["recovery"] = nil
			valid("null-recovery", body, false)
		})
	}
}

func recoverySchemaBody(kind string) map[string]any {
	state, outcome := "aborted", "aborted"
	if kind == "pending" {
		state, outcome = "draining", "draining"
	}
	result := map[string]any{"outcome": outcome, "recovery": map[string]any{"recovery_id": "11111111-1111-4111-8111-111111111111", "playback_attempt_id": "original-attempt", "session_id": "22222222-2222-4222-8222-222222222222", "state": state, "reason": "owner_lost"}}
	if kind == "start" {
		result["outcome"] = "adaptation_unavailable"
		result["protocol_version"] = float64(3)
		result["server_features"] = []any{}
		result["terminal"] = map[string]any{"reason": "playback_owner_lost", "message": "Playback ended after its server owner was lost.", "retryable": false}
	}
	return result
}
func cloneRecoverySchemaBody(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var copy map[string]any
	if err := json.Unmarshal(encoded, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}
