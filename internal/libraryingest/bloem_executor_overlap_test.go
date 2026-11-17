package libraryingest

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// NativeLibraryScanMode gives Silo's overlap fixture (executor_overlap_test.go)
// the finite local reader Bloem's storage ingest asks for. The overlap fixture
// owns library 7; the embedded scanner's older fixture admits only library 5
// and must not be widened for these tests.
func (s *gatedSubtreeScanner) NativeLibraryScanMode(ctx context.Context, id int) (*models.MediaFolder, bool, error) {
	if ctx == nil || s == nil || id != 7 {
		return nil, false, &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return &models.MediaFolder{ID: 7, Type: "movies", Enabled: true, Paths: []string{"/tv"}}, false, nil
}
