package notifications

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func inboxPageDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	admin, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "notification_inbox_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.WithoutCancel(t.Context()), "DROP SCHEMA "+quoted+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 4
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if _, err = p.Exec(t.Context(), `CREATE TABLE notification_deliveries (LIKE public.notification_deliveries INCLUDING ALL); CREATE TABLE notification_preferences (LIKE public.notification_preferences INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestNotificationInboxCutoffReplayAndTies(t *testing.T) {
	p := inboxPageDB(t)
	r := NewDeliveryRepository(p)
	ctx := t.Context()
	profile := uuid.NewString()
	other := uuid.NewString()
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	insert := func(id, profile string, at time.Time) {
		t.Helper()
		_, err := p.Exec(ctx, `INSERT INTO notification_deliveries(id,user_id,profile_id,type,reason_flags,status,created_at) VALUES($1,1,$2,'request.approved','{}','pending',$3)`, id, profile, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	low := "00000000-0000-0000-0000-000000000001"
	high := "00000000-0000-0000-0000-000000000002"
	fresh := "00000000-0000-0000-0000-000000000003"
	insert(low, profile, stamp)
	insert(high, profile, stamp)
	insert(uuid.NewString(), other, stamp)
	cutoff, err := r.InboxCutoff(ctx, profile)
	if err != nil || cutoff.ID != high {
		t.Fatalf("%+v %v", cutoff, err)
	}
	first, more, err := r.ListInboxWindow(ctx, profile, false, 1, nil, cutoff)
	if err != nil || len(first) != 1 || !more || first[0].ID != high {
		t.Fatalf("%+v %v %v", first, more, err)
	}
	insert(fresh, profile, stamp.Add(time.Microsecond))
	second, more, err := r.ListInboxWindow(ctx, profile, false, 1, &Cursor{CreatedAt: first[0].CreatedAt, ID: first[0].ID}, cutoff)
	if err != nil || len(second) != 1 || more || second[0].ID != low {
		t.Fatalf("%+v %v %v", second, more, err)
	}
	for n := range 2 {
		changed, err := r.MarkReadThrough(ctx, profile, cutoff)
		if err != nil || changed != int64(2*(1-n)) {
			t.Fatalf("changed=%d err=%v", changed, err)
		}
	}
	for _, check := range []struct {
		profile string
		want    int
	}{{profile, 1}, {other, 1}} {
		count, err := r.UnreadCount(ctx, check.profile)
		if err != nil || count != check.want {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
	empty, err := r.InboxCutoff(ctx, uuid.NewString())
	if err != nil || empty.ID != "" {
		t.Fatal(empty, err)
	}
	if changed, err := r.MarkReadThrough(ctx, profile, empty); err != nil || changed != 0 {
		t.Fatal(changed, err)
	}
}
func TestNotificationPreferencePatchConcurrentFields(t *testing.T) {
	p := inboxPageDB(t)
	r := NewPreferencesRepository(p)
	profile := uuid.NewString()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, patch := range []PreferencePatch{{Enabled: new(false)}, {NotifyFavorites: new(false)}} {
		wg.Go(func() { _, err := r.Patch(t.Context(), profile, patch); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	value, err := r.Get(t.Context(), profile)
	if err != nil || value.Enabled || value.NotifyFavorites || !value.NotifyWatchlist || !value.NotifyNextUp {
		t.Fatalf("%+v %v", value, err)
	}
}
