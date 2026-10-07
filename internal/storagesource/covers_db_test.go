package storagesource

import (
	"testing"
	"time"
)

func TestCoverClaimsLeaseAndCompleteAtTheirRevision(t *testing.T) {
	r, _, location, ref := fixtureReference(t)
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
	claims, err := r.ClaimCovers(t.Context(), 10, time.Minute)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims = %+v %v", claims, err)
	}
	claim := claims[0]
	if claim.ContentID != "existing-book" || claim.FolderID != 91001 || claim.LocationID != location.ID || claim.CoverEntryID != "cover/book" || claim.CoverRevision != "c1" {
		t.Fatalf("claim = %+v", claim)
	}
	// A claimed cover is not handed out again while its lease runs.
	if again, err := r.ClaimCovers(t.Context(), 10, time.Minute); err != nil || len(again) != 0 {
		t.Fatalf("claimed cover handed out twice: %+v %v", again, err)
	}
	// The cover changes before the fetch finishes: the old revision does not
	// complete it, and it is pending again once the lease expires.
	execSQL(t, r.pool, `UPDATE bloem_storage_file_refs SET cover_revision='c2' WHERE media_file_id=91001`)
	if err := r.MarkCoverFetched(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_file_refs SET cover_claimed_until = now() - interval '1 second'`)
	claims, err = r.ClaimCovers(t.Context(), 10, time.Minute)
	if err != nil || len(claims) != 1 || claims[0].CoverRevision != "c2" {
		t.Fatalf("changed cover not pending: %+v %v", claims, err)
	}
	if err := r.MarkCoverFetched(t.Context(), claims[0]); err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_file_refs SET cover_claimed_until = now() - interval '1 second'`)
	if claims, err := r.ClaimCovers(t.Context(), 10, time.Minute); err != nil || len(claims) != 0 {
		t.Fatalf("fetched cover still pending: %+v %v", claims, err)
	}
}
