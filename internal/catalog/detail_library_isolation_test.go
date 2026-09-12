package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// One canonical item can have files in different libraries (e.g. a movie
// re-scanned into a second folder). Alternating two organizations' library
// scopes against the same shared DetailService must never let one leak the
// other's file metadata: movie, episode and extra files all stay within the
// viewer's selected access.Scope library set.
func TestSharedTitleDetailsKeepFileMetadataWithinLibrary(t *testing.T) {
	f := newVersionsFixture(t)
	pool := f.svc.itemRepo.pool
	var second int
	if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name) VALUES('movies','other-library') RETURNING id`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		deleteCatalogTestMediaFolders(t, context.Background(), pool, second)
	})
	for _, kind := range []string{"movie", "series"} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, f.ids[kind], second); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO episode_libraries(episode_id,media_folder_id) VALUES($1,$2)`, f.ids["episode"], second); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"movie", "episode", "extra"} {
		files := f.files.files[f.ids[kind]]
		files[0].FilePath = "/library-a/only-a.mkv"
		files[1].MediaFolderID = second
		files[1].FilePath = "/library-b/only-b.mkv"
		// Each row simulates one organization's access.Scope: its
		// AllowedLibraryIDs names the single library that organization owns
		// or holds an entitlement for. Alternating back to the first
		// organization's scope proves the shared service instance carries no
		// state across viewers.
		for _, viewer := range []struct {
			library      int
			want, hidden string
		}{{f.library, "only-a", "only-b"}, {second, "only-b", "only-a"}, {f.library, "only-a", "only-b"}} {
			filter := AccessFilter{AllowedLibraryIDs: []int{viewer.library}}
			detail, err := f.svc.GetItemDetail(t.Context(), f.ids[kind], filter)
			if err != nil {
				t.Fatal(err)
			}
			versions, err := f.svc.GetItemVersions(t.Context(), f.ids[kind], filter)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []any{detail, versions} {
				body, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(body), viewer.want) || strings.Contains(string(body), viewer.hidden) {
					t.Fatalf("%s library %d: unexpected file projection %s", kind, viewer.library, body)
				}
			}
		}
	}
}
