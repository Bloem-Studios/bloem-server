package storagesource

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
)

func TestPublishedReadsRequireActiveSelectedRoot(t *testing.T) {
	for _, mutation := range []string{"membership", "root", "library", "configuration", "malformed-key"} {
		t.Run(mutation, func(t *testing.T) {
			r, _, location, ref := fixtureReference(t)
			tx, err := r.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			if err = AttachFilesTx(t.Context(), tx, 1, []FileRef{{MediaFileID: 91001, PersistedRef: ref, CoverEntryID: "cover/book", CoverRevision: "c1"}}); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			revision := artworkkey.StorageCoverRevision("cover/book", "c1")
			if _, _, err = r.FileReference(t.Context(), 91001, 91001); err != nil {
				t.Fatal(err)
			}
			if _, err = r.CoverReference(t.Context(), "existing-book", revision); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "membership":
				execSQL(t, r.pool, `UPDATE bloem_storage_file_roots SET enabled=false WHERE location_id=$1`, location.ID)
			case "root":
				execSQL(t, r.pool, `UPDATE bloem_storage_selected_roots SET enabled=false WHERE location_id=$1`, location.ID)
			case "library":
				execSQL(t, r.pool, `UPDATE media_folders SET enabled=false WHERE id=91001`)
			case "configuration":
				execSQL(t, r.pool, `UPDATE bloem_storage_file_refs SET configuration_revision=2 WHERE media_file_id=91001`)
			case "malformed-key":
				execSQL(t, r.pool, `UPDATE media_files SET file_path='bloem-storage://malformed' WHERE id=91001`)
			}
			if _, _, err = r.FileReference(t.Context(), 91001, 91001); !errors.Is(err, ErrReferenceConflict) {
				t.Fatalf("revoked file returned: %v", err)
			}
			if _, err = r.CoverReference(t.Context(), "existing-book", revision); !errors.Is(err, ErrReferenceConflict) {
				t.Fatalf("revoked cover returned: %v", err)
			}
		})
	}
}

// One source and cover published into two libraries yields both candidates in
// file order; an unusable candidate is left out without hiding the other, while
// exact redemption stays pinned to its own file and root.
func TestCoverReferencesListsEachLibraryPublication(t *testing.T) {
	for _, mutation := range []string{"none", "library", "membership", "malformed-key"} {
		t.Run(mutation, func(t *testing.T) {
			r, s, first, ref := fixtureReference(t)
			fixtureFolder(t, r.pool, 91002)
			second := fixtureLocation(t, r, s.Key, 91002)
			secondRef := PersistedRef{LocationID: second.ID, EntryID: ref.EntryID, Revision: ref.Revision, LogicalPath: ref.LogicalPath}
			path, err := CatalogLocation(second.ID, secondRef.EntryID)
			if err != nil {
				t.Fatal(err)
			}
			execSQL(t, r.pool, `INSERT INTO media_files(id,content_id,media_folder_id,file_path) VALUES(91002,'existing-book',91002,$1)`, path)
			tx, err := r.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			if err = AttachFilesTx(t.Context(), tx, 1, []FileRef{
				{MediaFileID: 91001, PersistedRef: ref, CoverEntryID: "cover/book", CoverRevision: "c1"},
				{MediaFileID: 91002, PersistedRef: secondRef, CoverEntryID: "cover/book", CoverRevision: "c1"},
			}); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			revision := artworkkey.StorageCoverRevision("cover/book", "c1")
			switch mutation {
			case "library":
				execSQL(t, r.pool, `UPDATE media_folders SET enabled=false WHERE id=91001`)
			case "membership":
				execSQL(t, r.pool, `UPDATE bloem_storage_file_roots SET enabled=false WHERE location_id=$1`, first.ID)
			case "malformed-key":
				execSQL(t, r.pool, `UPDATE media_files SET file_path='bloem-storage://malformed' WHERE id=91001`)
			}
			candidates, err := r.CoverReferences(t.Context(), "existing-book", revision)
			if err != nil {
				t.Fatal(err)
			}
			want := []int{91001, 91002}
			if mutation != "none" {
				want = []int{91002}
			}
			if len(candidates) != len(want) {
				t.Fatalf("candidates=%+v want files %v", candidates, want)
			}
			for i, c := range candidates {
				if c.MediaFileID != want[i] || c.Location.FolderID != want[i] || c.Cover.EntryID != "cover/book" {
					t.Fatalf("candidate %d=%+v want file %d", i, c, want[i])
				}
				exact, err := r.CoverReferenceForFile(t.Context(), "existing-book", revision, c.MediaFileID, c.RootID)
				if err != nil || exact.MediaFileID != c.MediaFileID || exact.RootID != c.RootID || exact.Location != c.Location || exact.File != c.File || exact.Cover != c.Cover || exact.FilePath != c.FilePath {
					t.Fatalf("exact redemption of %d: %+v %v", c.MediaFileID, exact, err)
				}
			}
			if mutation != "none" {
				if _, err = r.CoverReferenceForFile(t.Context(), "existing-book", revision, 91001, candidates[0].RootID); !errors.Is(err, ErrReferenceConflict) {
					t.Fatalf("revoked first publication redeemed: %v", err)
				}
			}
		})
	}
}
