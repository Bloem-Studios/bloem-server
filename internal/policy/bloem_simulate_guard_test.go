package policy

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSimulateMarksCallerProvidedTenantFactsNonAuthoritative(t *testing.T) {
	raw, err := json.Marshal(ScopeInput{
		SchemaVersion:        1,
		Tenant:               validLegacyTenantFactsForPolicyTest(),
		UserID:               7,
		AccessPolicyRevision: 1,
		ProfileVerified:      true,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	result, err := Simulate(context.Background(), nil, SimulateRequest{Domain: DomainScope, Input: raw})
	if err != nil {
		t.Fatalf("Simulate() error: %v", err)
	}
	if !result.NonAuthoritative {
		t.Fatal("NonAuthoritative = false, want true")
	}
}
