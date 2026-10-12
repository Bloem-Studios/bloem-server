//go:build integration

package nativestorage

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/google/uuid"
)

func TestScanPagePinsCompleteSelectionGraphDB(t *testing.T) {
	f := newScanReadFixture(t)
	p := f.admit(t)
	// This root edit need not touch the scanned source or owner to revoke old I/O.
	if _, err := f.r.pool.Exec(t.Context(), `INSERT INTO bloem_storage_selected_roots(id,location_id,directory_id,name,logical_path) VALUES($1,$2,'other-selected-root','Other','')`, uuid.New(), f.location.ID); err != nil {
		t.Fatal(err)
	}
	opened, err := f.r.OpenScanEntry(t.Context(), p, 0)
	if opened != nil {
		defer opened.Close()
	}
	if !errors.Is(err, resourcetenancy.ErrResourceHidden) {
		t.Fatalf("edited graph admitted old page: %v", err)
	}
}
func TestScanPageRejectsCrossLocationLeaseDB(t *testing.T) {
	f := newScanReadFixture(t)
	f.lease.LocationID = uuid.New()
	if _, err := f.r.AdmitScanPage(context.Background(), f.location, f.source, f.lease, f.checkpoint, f.page); err == nil {
		t.Fatal("another location lease admitted")
	}
}
