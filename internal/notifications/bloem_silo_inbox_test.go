package notifications

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	evt "github.com/Silo-Server/silo-server/internal/events"
)

// siloInboxFixture is one profile's inbox: two Silo rows and two Bloem
// notices, oldest first (the ordering trigger gives each insert a later
// created_at).
func siloInboxFixture(t *testing.T) (*DeliveryRepository, *pgxpool.Pool, string) {
	t.Helper()
	p := inboxPageDB(t)
	profile := uuid.NewString()
	for _, row := range []struct{ id, kind string }{
		{"silo-1", DeliveryTypeRequestApproved}, {"notice-1", DeliveryTypeSystemAnnouncement},
		{"silo-2", DeliveryTypeRequestDeclined}, {"notice-2", DeliveryTypeSystemAlert},
	} {
		if _, err := p.Exec(t.Context(), `INSERT INTO notification_deliveries(id,user_id,profile_id,type,reason_flags,status) VALUES($1,1,$2,$3,'{}','delivered')`, row.id, profile, row.kind); err != nil {
			t.Fatal(err)
		}
	}
	return NewDeliveryRepository(p), p, profile
}

// Silo's routes on a Bloem server serve the inbox a Silo server would: the
// same rows, pages, counts and read effects, with Bloem's own notices left
// out. Each Silo-view method is read beside Silo's own method on the same
// inbox, so a page or count can differ only by the notices.
func TestSiloInboxPagesLeaveBloemNoticesOut(t *testing.T) {
	r, _, profile := siloInboxFixture(t)
	ctx := t.Context()
	all, err := r.InboxCutoff(ctx, 1, profile)
	if err != nil || all.ID != "notice-2" {
		t.Fatalf("Silo's cutoff = %+v, %v; want the newest row, notice-2", all, err)
	}
	cutoff, err := r.SiloInboxCutoff(ctx, 1, profile)
	if err != nil || cutoff.ID != "silo-2" {
		t.Fatalf("cutoff = %+v, %v; want the newest Silo row, silo-2", cutoff, err)
	}
	page, more, err := r.SiloInboxWindow(ctx, 1, profile, false, 1, nil, cutoff)
	if err != nil || !more || !slices.Equal(rowIDs(page), []string{"silo-2"}) {
		t.Fatalf("first page = %v more=%v err=%v; want [silo-2] and more", rowIDs(page), more, err)
	}
	page, more, err = r.SiloInboxWindow(ctx, 1, profile, false, 1, &Cursor{CreatedAt: page[0].CreatedAt, ID: page[0].ID}, cutoff)
	if err != nil || more || !slices.Equal(rowIDs(page), []string{"silo-1"}) {
		t.Fatalf("second page = %v more=%v err=%v; want [silo-1] and no more", rowIDs(page), more, err)
	}
	page, _, err = r.SiloInboxWindow(ctx, 1, profile, true, 10, nil, all)
	if err != nil || !slices.Equal(rowIDs(page), []string{"silo-2", "silo-1"}) {
		t.Fatalf("unread page through the newest row = %v, %v; want Silo's two rows", rowIDs(page), err)
	}
	only := uuid.NewString()
	if _, err := r.pool.Exec(ctx, `INSERT INTO notification_deliveries(id,user_id,profile_id,type,reason_flags,status,created_at) VALUES('lone',1,$1,$2,'{}','delivered',$3)`, only, DeliveryTypeSystemAlert, time.Now()); err != nil {
		t.Fatal(err)
	}
	if empty, err := r.SiloInboxCutoff(ctx, 1, only); err != nil || empty.ID != "" {
		t.Fatalf("cutoff of a notices-only inbox = %+v, %v; want empty", empty, err)
	}
}

func TestSiloInboxSyncCountsAndLookupsLeaveBloemNoticesOut(t *testing.T) {
	r, _, profile := siloInboxFixture(t)
	ctx := t.Context()
	if count, err := r.UnreadCount(ctx, 1, profile); err != nil || count != 4 {
		t.Fatalf("Silo's unread count = %d, %v; want 4", count, err)
	}
	synced, err := r.SiloListSync(ctx, 1, profile, nil, 10)
	if err != nil || !slices.Equal(rowIDs(synced), []string{"silo-1", "silo-2"}) {
		t.Fatalf("initial sync = %v, %v; want [silo-1 silo-2]", rowIDs(synced), err)
	}
	synced, err = r.SiloListSync(ctx, 1, profile, &Cursor{CreatedAt: synced[0].CreatedAt, ID: synced[0].ID}, 10)
	if err != nil || !slices.Equal(rowIDs(synced), []string{"silo-2"}) {
		t.Fatalf("sync after silo-1 = %v, %v; want [silo-2]", rowIDs(synced), err)
	}
	if count, err := r.SiloUnreadCount(ctx, 1, profile); err != nil || count != 2 {
		t.Fatalf("unread count = %d, %v; want 2", count, err)
	}
	if row, err := r.SiloGetByID(ctx, 1, profile, "notice-1"); err != nil || row != nil {
		t.Fatalf("a notice by id = %+v, %v; want absent", row, err)
	}
	if row, err := r.SiloGetByID(ctx, 1, profile, "silo-1"); err != nil || row == nil || row.ID != "silo-1" {
		t.Fatalf("a Silo row by id = %+v, %v; want silo-1", row, err)
	}
	for _, check := range []struct{ id, want string }{
		{"notice-1", DeliveryTypeSystemAnnouncement}, {"silo-1", DeliveryTypeRequestApproved}, {"absent", ""},
	} {
		if got, err := r.DeliveryType(ctx, 1, profile, check.id); err != nil || got != check.want {
			t.Errorf("type of %s = %q, %v; want %q", check.id, got, err, check.want)
		}
	}
}

// The socket snapshot is Silo's too: Silo's events socket is its only reader.
func TestSiloSocketSnapshotLeavesBloemNoticesOut(t *testing.T) {
	r, _, profile := siloInboxFixture(t)
	recent, err := r.RecentUnread(t.Context(), 1, profile, 25)
	if err != nil || !slices.Equal(rowIDs(recent), []string{"silo-2", "silo-1"}) {
		t.Fatalf("socket snapshot = %v, %v; want only Silo's rows [silo-2 silo-1]", rowIDs(recent), err)
	}
}

// A Silo client's "mark all read" marks Silo's rows, and leaves the notices
// it never saw unread.
func TestSiloMarkReadThroughLeavesBloemNoticesUnread(t *testing.T) {
	r, _, profile := siloInboxFixture(t)
	ctx := t.Context()
	all, err := r.InboxCutoff(ctx, 1, profile)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := r.SiloMarkReadThrough(ctx, 1, profile, all)
	if err != nil || changed != 2 {
		t.Fatalf("marked %d, %v; want Silo's 2 rows", changed, err)
	}
	if count, err := r.UnreadCount(ctx, 1, profile); err != nil || count != 2 {
		t.Fatalf("unread after Silo's mark-all = %d, %v; want the 2 notices still unread", count, err)
	}
	if count, err := r.SiloUnreadCount(ctx, 1, profile); err != nil || count != 0 {
		t.Fatalf("Silo's unread after its mark-all = %d, %v; want 0", count, err)
	}
	if changed, err := r.SiloMarkReadThrough(ctx, 1, profile, Cursor{}); err != nil || changed != 0 {
		t.Fatalf("empty cutoff marked %d, %v", changed, err)
	}
}

// Silo's events socket carries notification.created for Silo's rows only. A
// Silo server never publishes one for a Bloem notice, and Silo's apps would
// otherwise add a blank "System" row to their inbox and badge.
func TestSiloSocketCarriesOnlySiloRows(t *testing.T) {
	hub := evt.NewHub("test", nil)
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	system := NewSystem(inboxPageDB(t), mapSettingReader{}, nil, nil, nil, hub, nil, nil, nil, nil)
	dispatcher := system.dispatcher
	for _, kind := range []string{DeliveryTypeSystemAlert, DeliveryTypeSystemAnnouncement, DeliveryTypeRequestApproved} {
		if err := dispatcher.Dispatch(t.Context(), DeliveryRow{ID: "row-" + kind, UserID: 1, ProfileID: "p1", Type: kind, CreatedAt: time.Now()}); err != nil {
			t.Fatalf("dispatch %s: %v", kind, err)
		}
	}
	select {
	case env := <-events:
		var payload DeliveryRowPayload
		if err := json.Unmarshal(env.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if env.Event != EventNotificationCreated || payload.Type != DeliveryTypeRequestApproved {
			t.Fatalf("first event = %s %s; want notification.created for the Silo row only", env.Event, payload.Type)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the Silo row was not published")
	}
	select {
	case env := <-events:
		t.Fatalf("unexpected second event %s: %s", env.Event, env.Data)
	default:
	}
}
