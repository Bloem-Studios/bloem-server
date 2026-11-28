package apiv2

import (
	"context"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	catalogpkg "github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
)

// scopeRecordingWatch is fakeWatch that also keeps the options each watch
// read asked its access filter for.
type scopeRecordingWatch struct {
	fakeWatch
	opts []handlers.AccessFilterOptions
}

func (f *scopeRecordingWatch) ContextAccessFilter(ctx context.Context, opts handlers.AccessFilterOptions) (catalogpkg.AccessFilter, error) {
	f.opts = append(f.opts, opts)
	return f.fakeWatch.ContextAccessFilter(ctx, opts)
}

// TestBloemWatchStateScopesVersionsToLibrary: with
// catalog.scope_versions_to_library on, GET /watch/{id}?library_id= lists
// only that library's versions, as the catalog item and episode reads do.
// Silo's Apple TV app picks its file from this read, so without the scope an
// Alt Cuts episode played the standard copy from another library.
func TestBloemWatchStateScopesVersionsToLibrary(t *testing.T) {
	for _, tc := range []struct {
		name, setting, query string
		want                 bool
	}{
		{"on with a library", "true", "?library_id=2", true},
		{"on without a library", "true", "", false},
		{"off with a library", "false", "?library_id=2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _ := catalogDeps(t)
			watch := &scopeRecordingWatch{}
			deps.Watch = watch
			deps.CatalogSettings = fakeCatalogSettings{config.CatalogScopeVersionsToLibrarySettingKey: tc.setting}
			h := newTestHandler(t, deps)
			rec := do(t, h, http.MethodGet, "/api/v2/watch/movie:heat-1995"+tc.query, "", themeSongHeaders())
			if rec.Code != http.StatusOK {
				t.Fatalf("watch detail: %d %s", rec.Code, rec.Body)
			}
			if len(watch.opts) != 1 {
				t.Fatalf("access filter built %d times, want 1", len(watch.opts))
			}
			if got := watch.opts[0].ScopeFilesToLibrary; got != tc.want {
				t.Fatalf("ScopeFilesToLibrary = %v, want %v", got, tc.want)
			}
		})
	}
}
