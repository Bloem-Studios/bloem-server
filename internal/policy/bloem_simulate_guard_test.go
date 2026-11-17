package policy

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import "testing"

func TestGuardBenchmarkInputCarriesResolvedTenant(t *testing.T) {
	for _, domain := range []string{DomainScope, DomainPermission, DomainAction} {
		t.Run(domain, func(t *testing.T) {
			input, err := guardBenchmarkInput(domain)
			if err != nil {
				t.Fatalf("guardBenchmarkInput() error: %v", err)
			}
			doc, ok := input.(map[string]any)
			if !ok {
				t.Fatalf("guard input = %T, want a JSON object", input)
			}
			tenant, _ := doc["tenant"].(map[string]any)
			if tenant["present"] != true {
				t.Fatalf("guard input tenant = %#v, want a present tenant", doc["tenant"])
			}
			if domain == DomainScope {
				if ids, _ := doc["tenant_library_ids"].([]any); len(ids) == 0 {
					t.Fatalf("guard scope input tenant_library_ids = %#v, want libraries", doc["tenant_library_ids"])
				}
			}
		})
	}
}
