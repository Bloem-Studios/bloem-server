package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

// noticeInbox is one profile's inbox in its own schema: two Silo rows and
// two Bloem notices, oldest first, with a profile that has nothing.
type noticeInbox struct {
	pool    *pgxpool.Pool
	handler *NotificationsHandler
	events  <-chan evt.Envelope
	profile string
}

func newNoticeInbox(t *testing.T) noticeInbox {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	admin, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "silo_inbox_view_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+quoted+"; CREATE TABLE "+quoted+".notification_deliveries (LIKE public.notification_deliveries INCLUDING ALL)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.WithoutCancel(t.Context()), "DROP SCHEMA "+quoted+" CASCADE") })
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	inbox := noticeInbox{pool: pool, profile: uuid.NewString()}
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	for i, row := range []struct{ id, kind, body string }{
		{"silo-1", notifications.DeliveryTypeRequestApproved, ""},
		{"notice-1", notifications.DeliveryTypeSystemAnnouncement, `{"title":"Maintenance tonight","body":"The server restarts at ten.","severity":"info","dismissible":true}`},
		{"silo-2", notifications.DeliveryTypeRequestDeclined, ""},
		{"notice-2", notifications.DeliveryTypeSystemAlert, `{"title":"Storage is full","body":"Recordings stop.","severity":"warning","dismissible":true}`},
	} {
		var body any
		if row.body != "" {
			body = row.body
		}
		if _, err := pool.Exec(t.Context(), `INSERT INTO notification_deliveries(id,user_id,profile_id,type,reason_flags,status,created_at,body) VALUES($1,1,$2,$3,'{}','delivered',$4,$5::jsonb)`,
			row.id, inbox.profile, row.kind, start.Add(time.Duration(i)*time.Minute), body); err != nil {
			t.Fatal(err)
		}
	}
	hub := evt.NewHub("test", nil)
	events, unsubscribe := hub.Subscribe()
	t.Cleanup(unsubscribe)
	inbox.events = events
	inbox.handler = NewNotificationsHandler(&notifications.System{Deliveries: notifications.NewDeliveryRepository(pool)}, hub)
	return inbox
}

func payloadIDs(rows []notifications.DeliveryRowPayload) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

func wantNotFound(t *testing.T, what string, err error) {
	t.Helper()
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || apiErr.Code != "not_found" {
		t.Errorf("%s = %v; want Silo's 404 not_found", what, err)
	}
}

// Silo's /api/v2 notification routes read through BloemSiloInboxView. With a
// Bloem notice in the inbox they answer as a Silo server does: the notice is
// not listed, synced or counted, and fetching, displaying or reading it by ID
// is Silo's 404. Silo's own service, which Bloem's native routes use, still
// serves every row.
func TestSiloInboxViewAnswersAsSiloWithBloemNoticesPresent(t *testing.T) {
	inbox := newNoticeInbox(t)
	ctx := t.Context()
	view := BloemSiloInboxView{NotificationsHandler: inbox.handler}

	// Control: Silo's own service lists and counts the notices.
	page, err := inbox.handler.ListNotificationInbox(ctx, 1, inbox.profile, false, 25, nil, nil)
	if err != nil || len(page.Items) != 4 {
		t.Fatalf("Silo's service listed %v, %v; want all 4 rows", payloadIDs(page.Items), err)
	}

	page, err = view.ListNotificationInbox(ctx, 1, inbox.profile, false, 25, nil, nil)
	if err != nil || !slices.Equal(payloadIDs(page.Items), []string{"silo-2", "silo-1"}) || page.Through.ID != "silo-2" {
		t.Fatalf("list = %v through %s, %v; want [silo-2 silo-1] through silo-2", payloadIDs(page.Items), page.Through.ID, err)
	}
	rows, more, unread, err := view.SyncNotificationInbox(ctx, 1, inbox.profile, 50, nil)
	if err != nil || more || unread != 2 || !slices.Equal(payloadIDs(rows), []string{"silo-1", "silo-2"}) {
		t.Fatalf("sync = %v more=%v unread=%d, %v; want [silo-1 silo-2], 2 unread", payloadIDs(rows), more, unread, err)
	}
	if count, err := view.NotificationUnreadCount(ctx, 1, inbox.profile); err != nil || count != 2 {
		t.Fatalf("unread count = %d, %v; want 2", count, err)
	}
	_, err = view.GetNotificationInboxItem(ctx, 1, inbox.profile, "notice-1")
	wantNotFound(t, "get a notice", err)
	_, err = view.NotificationPushDisplay(ctx, 1, inbox.profile, "notice-1")
	wantNotFound(t, "Apple display of a notice", err)
	wantNotFound(t, "mark a notice read", view.MarkNotificationRead(ctx, 1, inbox.profile, "notice-1"))
	wantNotFound(t, "mark an absent row read", view.MarkNotificationRead(ctx, 1, inbox.profile, "absent"))
	if item, err := view.GetNotificationInboxItem(ctx, 1, inbox.profile, "silo-1"); err != nil || item.ID != "silo-1" {
		t.Fatalf("get a Silo row = %+v, %v", item, err)
	}

	// Mark all read through the newest row there is: Silo's rows only, and
	// Silo's read event.
	if err := view.MarkNotificationInboxThrough(ctx, 1, inbox.profile, notifications.Cursor{CreatedAt: time.Now(), ID: "notice-2"}); err != nil {
		t.Fatal(err)
	}
	select {
	case env := <-inbox.events:
		if env.Event != notifications.EventNotificationRead {
			t.Fatalf("event = %s; want notification.read", env.Event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification.read after a read-all that changed rows")
	}
	if count, err := view.NotificationUnreadCount(ctx, 1, inbox.profile); err != nil || count != 0 {
		t.Fatalf("Silo's unread after its read-all = %d, %v; want 0", count, err)
	}
	if count, err := inbox.handler.NotificationUnreadCount(ctx, 1, inbox.profile); err != nil || count != 2 {
		t.Fatalf("every unread after Silo's read-all = %d, %v; want the 2 notices", count, err)
	}
}

// bloemNoticeRoutes serves Bloem's native routes for one signed-in profile.
func bloemNoticeRoutes(h *NotificationsHandler, profile string) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := apimw.SetClaims(req.Context(), &auth.Claims{UserID: 1, Role: "user"})
			next.ServeHTTP(w, req.WithContext(apimw.SetProfileID(ctx, profile)))
		})
	})
	r.Get("/notifications", h.HandleBloemNotificationList)
	r.Get("/notifications/unread-count", h.HandleBloemNotificationUnreadCount)
	r.Post("/notifications/read-all", h.HandleBloemNotificationReadAll)
	r.Post("/notifications/{id}/read", h.HandleBloemNotificationRead)
	return r
}

func serveNotice(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func nativeUnread(t *testing.T, h http.Handler) int {
	t.Helper()
	rec := serveNotice(t, h, http.MethodGet, "/notifications/unread-count", "")
	var body struct {
		Count *int `json:"count"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Count == nil {
		t.Fatalf("unread-count = %d %s", rec.Code, rec.Body)
	}
	return *body.Count
}

// Bloem's own web reads Bloem's native inbox, which keeps every notice: the
// list carries a notice's title and text, the unread count includes it, and
// the viewer can mark it read alone or with everything else.
func TestBloemNativeInboxCountsAndReadsNotices(t *testing.T) {
	inbox := newNoticeInbox(t)
	routes := bloemNoticeRoutes(inbox.handler, inbox.profile)

	rec := serveNotice(t, routes, http.MethodGet, "/notifications", "")
	var page struct {
		Items      []notifications.DeliveryRowPayload `json:"items"`
		ReadCutoff string                             `json:"read_cutoff"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Items) != 4 {
		t.Fatalf("native list = %d %s; want all 4 rows", rec.Code, rec.Body)
	}
	if notice := page.Items[0]; notice.ID != "notice-2" || notice.Title != "Storage is full" || notice.Body != "Recordings stop." {
		t.Fatalf("newest row = %+v; want notice-2 with its title and text", notice)
	}
	if got := nativeUnread(t, routes); got != 4 {
		t.Fatalf("native unread = %d; want 4", got)
	}

	// A notice is read without a notification.read on Silo's socket.
	if rec := serveNotice(t, routes, http.MethodPost, "/notifications/notice-1/read", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("read a notice = %d %s", rec.Code, rec.Body)
	}
	select {
	case env := <-inbox.events:
		t.Fatalf("reading a notice published %s %s on Silo's socket", env.Event, env.Data)
	default:
	}
	// A Silo row is read through Silo's path, which tells Silo's clients.
	if rec := serveNotice(t, routes, http.MethodPost, "/notifications/silo-1/read", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("read a Silo row = %d %s", rec.Code, rec.Body)
	}
	select {
	case env := <-inbox.events:
		if env.Event != notifications.EventNotificationRead {
			t.Fatalf("event = %s; want notification.read", env.Event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a Silo row sent no notification.read")
	}
	if got := nativeUnread(t, routes); got != 2 {
		t.Fatalf("native unread after two reads = %d; want 2", got)
	}
	if rec := serveNotice(t, routes, http.MethodPost, "/notifications/absent/read", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("read an absent row = %d %s; want 404", rec.Code, rec.Body)
	}

	for _, bad := range []string{``, `{}`, `{"through":"not a cursor"}`} {
		if rec := serveNotice(t, routes, http.MethodPost, "/notifications/read-all", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("read-all %q = %d %s; want 400", bad, rec.Code, rec.Body)
		}
	}
	if rec := serveNotice(t, routes, http.MethodPost, "/notifications/read-all", `{"through":"`+page.ReadCutoff+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("read-all = %d %s", rec.Code, rec.Body)
	}
	if got := nativeUnread(t, routes); got != 0 {
		t.Fatalf("native unread after read-all = %d; want 0", got)
	}

	// An empty inbox's cutoff marks nothing.
	empty := bloemNoticeRoutes(inbox.handler, uuid.NewString())
	rec = serveNotice(t, empty, http.MethodGet, "/notifications", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Items) != 0 {
		t.Fatalf("empty list = %s, %v", rec.Body, err)
	}
	if rec := serveNotice(t, empty, http.MethodPost, "/notifications/read-all", `{"through":"`+page.ReadCutoff+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("read-all of an empty inbox = %d %s; want 204", rec.Code, rec.Body)
	}
}
