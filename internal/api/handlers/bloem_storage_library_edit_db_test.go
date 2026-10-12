//go:build integration

package handlers

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

// The edit form sends the current type even when only the name changes.
// Native movie/audio libraries must accept that without changing source kind.
func TestStorageLibraryEditPreservesKindDB(t *testing.T) {
	pool := task3LibraryDatabase(t)
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var installation int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id,runtime_generation) VALUES('storage-edit','1','/synthetic',$1,1) RETURNING id`, owner).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, installation, owner); err != nil {
		t.Fatal(err)
	}
	h := NewLibraryHandler(catalog.NewFolderRepository(pool), nil, nil, pool, nil)
	h.ScanQueue = noopLibraryScanQueue{}
	for _, tc := range []struct{ stored, alias string }{
		{"movies", " Movies "}, {"audiobooks", "Audiobook"}, {"ebooks", "Ebook"},
	} {
		t.Run(tc.stored, func(t *testing.T) {
			source, err := storagesource.NewRepository(pool).CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, InstallationID: &installation, PluginID: "storage-edit", ProviderSourceID: tc.stored, RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			var id int
			if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name,owner_id) VALUES($1,'Before',$2) RETURNING id`, tc.stored, owner).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,$3)`, uuid.New(), source.Key, id); err != nil {
				t.Fatal(err)
			}
			name := "After"
			if _, err := h.UpdateLibrary(t.Context(), id, 0, LibraryUpdateRequest{Name: &name, Type: &tc.alias}); err != nil {
				t.Fatalf("edit with unchanged kind: %v", err)
			}
			changed := "series"
			_, err = h.UpdateLibrary(t.Context(), id, 0, LibraryUpdateRequest{Type: &changed})
			var api *APIError
			if !errors.As(err, &api) || api.Status != http.StatusBadRequest || api.Field != "type" {
				t.Fatalf("kind change must be refused: %v", err)
			}
			var gotName, gotKind string
			if err := pool.QueryRow(t.Context(), `SELECT name,type FROM media_folders WHERE id=$1`, id).Scan(&gotName, &gotKind); err != nil || gotName != name || gotKind != tc.alias {
				t.Fatalf("saved edit = %q/%q: %v", gotName, gotKind, err)
			}
		})
	}
}
