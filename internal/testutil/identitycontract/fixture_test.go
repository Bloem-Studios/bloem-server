package identitycontract

import (
	"encoding/json"
	"net/http"
	"testing"
)

const refWrongPassword = "fixture-wrong-password-002"

func writeFixtureJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func requireSingleFailure(t *testing.T, report Report, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatal("a violating listener passed the identity contract")
	}
	failed := report.FailedCases()
	if len(failed) != 1 || failed[0] != want {
		t.Fatalf("failed cases = %v, want exactly [%q]; report=%s", failed, want, report.JSON())
	}
}
