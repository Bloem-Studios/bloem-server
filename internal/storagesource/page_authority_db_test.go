package storagesource

import (
	"testing"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

func TestApplyPageAuthorizedRequiresAuthority(t *testing.T) {
	r, _, lease, checkpoint := scanFixture(t)
	page := &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, Complete: true}
	if err := r.ApplyPageAuthorized(t.Context(), lease, checkpoint, page, nil, nil); err == nil {
		t.Fatal("missing authority allowed page publication")
	}
	var entries, cursors int
	if err := r.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM bloem_storage_entries),(SELECT count(*) FROM bloem_storage_scan_cursors)`).Scan(&entries, &cursors); err != nil {
		t.Fatal(err)
	}
	if entries != 0 || cursors != 0 {
		t.Fatalf("unauthorized page committed entries=%d cursors=%d", entries, cursors)
	}
}
