package executor

import (
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
)

func TestRequiredProfileListAcceptance(t *testing.T) {
	if os.Getenv("SILO_SCENARIO_REQUIRED") != "1" {
		t.Skip("run make test-scenario-profile-pairing for required acceptance")
	}
	catalogs, err := scenariocatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	pilot, err := scenariocatalog.ProfileListAcceptance(catalogs)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(DatabaseEnv) == "" {
		t.Fatal(DatabaseEnv + " is required; acceptance cannot skip its database")
	}
	results := RunAll(t, pilot)
	for _, result := range results {
		if !result.Passed() {
			t.Errorf("required %s/%s did not pass: skipped=%q failures=%v", result.Scenario, result.Transport, result.Skipped, result.Failures)
		}
	}
	if len(results) != 2*len(scenariocatalog.RequiredProfileListScenarios) {
		t.Fatalf("required paired exchanges = %d, want %d", len(results), 2*len(scenariocatalog.RequiredProfileListScenarios))
	}
	if err := WriteReport(results); err != nil {
		t.Fatal(err)
	}
}
