package executor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
)

// Run the real catalog executor without a database. A failure must identify
// the missing prerequisite, not send validation requests to a known dead store
// and misreport its 503 as a contract regression or silently skip the case.
func TestLifecyclePrerequisiteMissingDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	report := filepath.Join(t.TempDir(), "report.json")
	selector := "^TestScenarioCatalogs$/api/api-v1-auth/POST_/api/v1/auth/(setup|signup)_#0$/(setup.missing_fields|setup.malformed_json|signup.missing_fields)$"
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run="+selector, "-test.timeout=25s")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, DatabaseEnv+"=") && !strings.HasPrefix(value, "SILO_SCENARIO_REPORT=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, DatabaseEnv+"=", "SILO_SCENARIO_REPORT="+report)
	output, err := cmd.CombinedOutput()
	if err == nil || ctx.Err() != nil {
		t.Fatalf("missing prerequisite must fail promptly: err=%v context=%v\n%s", err, ctx.Err(), output)
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("missing prerequisite report: %v\n%s", err, output)
	}
	var results []Result
	if err := json.Unmarshal(raw, &results); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{}
	for _, id := range []string{"setup.missing_fields", "setup.malformed_json", "signup.missing_fields"} {
		wanted[id+"/v1"], wanted[id+"/v2"] = false, false
	}
	for _, result := range results {
		seen, ok := wanted[result.ID]
		if !ok || seen || result.Skipped != "" {
			t.Fatalf("unexpected, duplicate or skipped result: %+v", result)
		}
		wanted[result.ID] = true
		if result.Transport == "v1" {
			if len(result.Failures) != 1 || !strings.Contains(result.Failures[0], "requires a reachable lifecycle store") || !strings.Contains(result.Failures[0], DatabaseEnv) {
				t.Errorf("missing readiness was not reported explicitly: %+v", result)
			}
		} else if !result.Passed() {
			t.Errorf("independent v2 input validation failed: %+v", result)
		}
	}
	for id, seen := range wanted {
		if !seen {
			t.Errorf("missing result %s", id)
		}
	}
}

func TestLifecyclePrerequisiteSelection(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		outage, want       bool
	}{
		{"setup", "POST", "/api/v1/auth/setup", false, true},
		{"signup query", "POST", "/api/v1/auth/signup/?source=fixture", false, true},
		{"outage", "POST", "/api/v1/auth/setup", true, false},
		{"public discovery", "GET", "/api/v1/auth/providers", false, false},
		{"credential exchange", "POST", "/api/v1/auth/login", false, false},
		{"independent v2 validation", "POST", "/api/v2/auth/setup", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := scenariocatalog.Scenario{Request: scenariocatalog.Request{Path: tc.path}}
			if tc.outage {
				s.Requires = []string{"database_unavailable"}
			}
			if got := lifecycleStoreRequired(s, "v1", tc.method); got != tc.want {
				t.Fatalf("lifecycle prerequisite = %v, want %v", got, tc.want)
			}
		})
	}
	s := scenariocatalog.Scenario{
		Request:       scenariocatalog.Request{Path: "/api/v1/auth/providers"},
		Then:          []scenariocatalog.Step{{Method: "POST", Request: scenariocatalog.Request{Path: "/api/v1/auth/signup"}}},
		V2Expectation: &scenariocatalog.V2Expectation{Method: "GET", Request: scenariocatalog.Request{Path: "/api/v2/auth/providers"}},
	}
	if !lifecycleStoreRequired(s, "v1", "GET") {
		t.Fatal("lifecycle follow-up must require readiness before the first exchange")
	}
	if lifecycleStoreRequired(v2Scenario(s), "v2", "GET") {
		t.Fatal("v2 inherited a v1 follow-up prerequisite")
	}
}
