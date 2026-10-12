package storagesource

import (
	"context"
	"testing"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
)

func TestApplyPageInvokesPublisherOnCompletingEmptyPageDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	lease := scopedBegin(t, r, l)
	cp, more, err := r.NextDirectory(t.Context(), lease)
	if err != nil || !more {
		t.Fatal(err)
	}
	calls := 0
	publisher := func(context.Context, pgx.Tx, []*storagev1.Entry, []string) error { calls++; return nil }
	page := &storagev1.ListResponse{Complete: true}
	if err := r.ApplyPage(t.Context(), lease, cp, page, publisher); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("publisher calls=%d", calls)
	}
	if err := r.ApplyPage(t.Context(), lease, cp, page, publisher); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("replayed publisher calls=%d", calls)
	}
}
