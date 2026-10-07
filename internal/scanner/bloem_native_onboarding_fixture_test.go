package scanner

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The caller must query current_database before any fixture write. A parsed
// DSN alone is insufficient; accept only the exact fresh private UUID clone,
// excluding the input template even if it also has a private-test prefix.
func onboardingFixtureCloneMatches(actual, expected, template string) bool {
	const prefix = "bloem_storage_test_ingest_"
	if actual != expected || actual == template || !strings.HasPrefix(actual, prefix) {
		return false
	}
	id, err := uuid.Parse(strings.TrimPrefix(actual, prefix))
	return err == nil && id != uuid.Nil
}
func TestNativeOnboardingCloneIdentityGate(t *testing.T) {
	name := "bloem_storage_test_ingest_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for _, tt := range []struct {
		name, actual, expected, template string
		want                             bool
	}{
		{"exact", name, name, "bloem_storage_test_historical", true},
		{"template", name, name, name, false},
		{"different", name + "a", name, "bloem_storage_test_historical", false},
		{"wrongPrefix", "postgres", "postgres", "bloem_storage_test_historical", false},
		{"missingUUID", "bloem_storage_test_ingest_", "bloem_storage_test_ingest_", "bloem_storage_test_historical", false},
		{"nilUUID", "bloem_storage_test_ingest_00000000000000000000000000000000", "bloem_storage_test_ingest_00000000000000000000000000000000", "bloem_storage_test_historical", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := onboardingFixtureCloneMatches(tt.actual, tt.expected, tt.template); got != tt.want {
				t.Fatalf("clone identity gate=%t want=%t", got, tt.want)
			}
		})
	}
}
