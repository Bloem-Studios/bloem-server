package storagesource

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
)

func TestCoverReferenceFindsTheBooksCoverAtItsRevision(t *testing.T) {
	r, source, location, ref := fixtureReference(t)
	tx, err := r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := AttachFilesTx(t.Context(), tx, 1, []FileRef{{MediaFileID: 91001, PersistedRef: ref, CoverEntryID: "cover/book", CoverRevision: "c1"}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := r.CoverReference(t.Context(), "existing-book", artworkkey.StorageCoverRevision("cover/book", "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaFileID != 91001 || got.FolderID != 91001 || got.ContentID != "existing-book" || got.Location != location || got.Source.Key != source.Key ||
		got.Cover != (PersistedRef{LocationID: location.ID, EntryID: "cover/book", Revision: "c1"}) {
		t.Fatalf("cover reference = %+v", got)
	}
	// A URL issued for an earlier cover no longer resolves once the cover changes.
	execSQL(t, r.pool, `UPDATE bloem_storage_file_refs SET cover_revision='c2' WHERE media_file_id=91001`)
	if _, err := r.CoverReference(t.Context(), "existing-book", artworkkey.StorageCoverRevision("cover/book", "c1")); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("stale cover resolved: %v", err)
	}
	if _, err := r.CoverReference(t.Context(), "other-book", artworkkey.StorageCoverRevision("cover/book", "c2")); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("cover resolved for another book: %v", err)
	}
	// A disabled source serves no covers.
	execSQL(t, r.pool, `UPDATE bloem_storage_sources SET enabled=false WHERE key=$1`, source.Key)
	if _, err := r.CoverReference(t.Context(), "existing-book", artworkkey.StorageCoverRevision("cover/book", "c2")); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("disabled source served a cover: %v", err)
	}
}
