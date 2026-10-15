package preview_test

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/preview"
)

func TestPreviewEventsPersistOnlyDailyCounters(t *testing.T) {
	ctx, pool := previewTestDB(t)
	events := preview.NewEvents(pool)
	for _, name := range []string{"cta_shown", "cta_clicked", "session_ended"} {
		if err := events.Increment(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := events.Increment(ctx, "arbitrary"); err == nil {
		t.Fatal("arbitrary event accepted")
	}
	var columns, count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='bloem_preview_event_counts'`).Scan(&columns); err != nil || columns != 3 {
		t.Fatalf("counter schema has identity/payload fields: %d %v", columns, err)
	}
	if err := pool.QueryRow(ctx, `SELECT sum(count)::int FROM bloem_preview_event_counts`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("counters: %d %v", count, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM bloem_preview_event_counts`); err != nil {
		t.Fatal(err)
	}
}
