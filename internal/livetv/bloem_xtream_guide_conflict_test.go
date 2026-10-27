package livetv

import (
	"errors"
	"testing"
)

// A synchronization started while another holds the source is a conflict,
// and a disabled source is refused as an invalid argument before any claim.
func TestBloemXtreamGuideSyncConflictsWithARunningSyncAndRefusesADisabledSource(t *testing.T) {
	pool := bloemXtreamTestPool(t)
	f := newXtreamGuideDBFixture(t, pool)
	if _, err := f.store.claimXtreamGuideSync(t.Context(), f.source); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SyncGuideSource(t.Context(), f.source.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("sync while one runs = %v; want ErrConflict", err)
	}

	if _, err := pool.Exec(t.Context(), `UPDATE livetv_guide_sources SET enabled=false,status='ready' WHERE id=$1`, f.source.ID); err != nil {
		t.Fatal(err)
	}
	err := f.service.SyncGuideSource(t.Context(), f.source.ID)
	if !errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrConflict) {
		t.Fatalf("sync of a disabled source = %v; want ErrInvalidArgument", err)
	}
}
