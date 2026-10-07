package libraryingest

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Explicit finite fixture ID from unchanged executor_test.go, not a default map.
// Neither fake can claim native fixtures. No test names are inspected.
func (s *finalizeRecordingScanner) NativeLibraryScanMode(ctx context.Context, id int) (*models.MediaFolder, bool, error) {
	if ctx == nil || s == nil || id != 5 {
		return nil, false, &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	return &models.MediaFolder{ID: 5, Type: "ebooks", Enabled: true}, false, nil
}
func (s *settleStubScanner) NativeLibraryScanMode(ctx context.Context, id int) (*models.MediaFolder, bool, error) {
	if ctx == nil || s == nil || id != 5 {
		return nil, false, &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	return &models.MediaFolder{ID: 5, Type: "series", Enabled: true}, false, nil
}

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
