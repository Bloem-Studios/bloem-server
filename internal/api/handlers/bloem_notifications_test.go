package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

func bloemTestRow(id string, at time.Time) notifications.DeliveryRowPayload {
	return notifications.DeliveryRowPayload{ID: id, Type: "system.alert", CreatedAt: at}
}

// The reason this surface exists is that Silo's v2 projection drops the alert
// fields. If the native page ever stopped carrying them the surface would be
// pointless, and the failure would be invisible: the endpoint would still
// answer 200 with a well-formed page.
func TestBloemInboxPageCarriesTheAlertFields(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	row := bloemTestRow("d1", at)
	dismissible := false
	row.Title, row.Body, row.Severity = "Storage is full", "Recordings will stop", "critical"
	row.Deeplink, row.ImageURL, row.Dismissible = "bloem://settings/storage", "https://example.test/a.png", &dismissible
	row.CTA = &notifications.AlertCTA{Label: "Manage", URL: "https://example.test/manage"}

	raw, err := json.Marshal(bloemInboxPageOf(NotificationInboxPageView{Items: []notifications.DeliveryRowPayload{row}}))
	if err != nil {
		t.Fatalf("marshalling the page: %v", err)
	}
	var page struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("unmarshalling the page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %d, want 1: %s", len(page.Items), raw)
	}
	for _, field := range []string{"title", "body", "severity", "deeplink", "image_url", "dismissible", "cta"} {
		if _, ok := page.Items[0][field]; !ok {
			t.Errorf("the native inbox row dropped %q, which is what this surface exists to carry: %s", field, raw)
		}
	}
}

// An empty inbox must be an empty list, not a null: a client that has to
// handle both spellings will eventually handle one of them wrong.
func TestBloemInboxPagesAreNeverNull(t *testing.T) {
	for name, raw := range map[string][]byte{
		"list": mustMarshal(t, bloemInboxPageOf(NotificationInboxPageView{})),
		"sync": mustMarshal(t, bloemSyncPageOf(nil, false, 0, nil)),
	} {
		var body struct {
			Items *[]json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if body.Items == nil || len(*body.Items) != 0 {
			t.Errorf("%s: items = %v, want []: %s", name, body.Items, raw)
		}
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling %T: %v", v, err)
	}
	return raw
}

// The next cursor carries both positions a stable traversal needs. Round-trip
// them, because a cursor that decodes to a different place than it encoded
// pages a client silently through a shifting list.
func TestBloemInboxCursorRoundTrips(t *testing.T) {
	want := bloemInboxCursor{
		Before:  notifications.Cursor{CreatedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), ID: "d9"},
		Through: notifications.Cursor{CreatedAt: time.Date(2026, 9, 14, 8, 30, 0, 0, time.UTC), ID: "d1"},
	}
	got, ok := decodeBloemInboxCursor(want.encode())
	if !ok {
		t.Fatalf("a cursor this package encoded did not decode: %q", want.encode())
	}
	if !got.Before.CreatedAt.Equal(want.Before.CreatedAt) || got.Before.ID != want.Before.ID {
		t.Errorf("before = %+v, want %+v", got.Before, want.Before)
	}
	if !got.Through.CreatedAt.Equal(want.Through.CreatedAt) || got.Through.ID != want.Through.ID {
		t.Errorf("through = %+v, want %+v", got.Through, want.Through)
	}
}

func TestBloemInboxCursorRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"", "not-a-cursor", "~", "abc~", "~abc", notifications.Cursor{ID: "d1"}.Encode()} {
		if _, ok := decodeBloemInboxCursor(raw); ok {
			t.Errorf("decoded %q, which is not a cursor this package minted", raw)
		}
	}
}

// A bad cursor is the client's mistake, and it must be told so rather than
// served page one as though nothing happened. The handler rejects it before it
// reaches the service, which is what lets this run without one.
func TestBloemInboxRejectsABadCursorBeforeReachingTheService(t *testing.T) {
	h := &NotificationsHandler{}
	for path, handler := range map[string]http.HandlerFunc{
		"/notifications":      h.HandleBloemNotificationList,
		"/notifications/sync": h.HandleBloemNotificationSync,
	} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, path+"?cursor=nonsense", nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", path, rec.Code, rec.Body.String())
		}
	}
}

// An empty sync page hands the cursor back. Losing it would make a client
// that polls during a quiet period restart its traversal from the newest
// snapshot and re-notify the viewer about rows it has already seen.
func TestBloemSyncPageKeepsTheCursorWhenNothingIsNew(t *testing.T) {
	since := notifications.Cursor{CreatedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), ID: "d4"}
	page := bloemSyncPageOf(nil, false, 3, &since)
	if page.Page.NextCursor != since.Encode() {
		t.Errorf("next_cursor = %q, want the cursor that was sent (%q)", page.Page.NextCursor, since.Encode())
	}
	if page.InitialSnapshot {
		t.Error("a sync resumed from a cursor is not an initial snapshot")
	}
	if page.UnreadCount != 3 {
		t.Errorf("unread_count = %d, want 3", page.UnreadCount)
	}
}

// A first sync says so, so a client can tell its own backlog from a gap it
// missed while offline.
func TestBloemSyncPageMarksTheInitialSnapshot(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	page := bloemSyncPageOf([]notifications.DeliveryRowPayload{bloemTestRow("d1", at)}, true, 1, nil)
	if !page.InitialSnapshot {
		t.Error("a sync with no cursor is the initial snapshot")
	}
	if !page.Page.HasMore || page.Page.NextCursor == "" {
		t.Errorf("page = %+v, want a cursor and has_more", page.Page)
	}
}
