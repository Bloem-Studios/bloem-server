package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type fakeDismissStore struct {
	dismissed   []string
	transition  bool
	err         error
	exists      bool
	seenProfile string
}

func (f *fakeDismissStore) Dismiss(_ context.Context, profileID, id string) (bool, error) {
	f.seenProfile = profileID
	f.dismissed = append(f.dismissed, id)
	return f.transition, f.err
}

func (f *fakeDismissStore) Exists(context.Context, string, string) (bool, error) {
	return f.exists, nil
}

func dismissRequest(id string) *http.Request {
	return withChiID(downloadTestRequest(http.MethodPost, "/notifications/"+id+"/dismiss", nil, 7, "profile-a", ""), id)
}

func TestHandleDismissTransitions(t *testing.T) {
	store := &fakeDismissStore{transition: true}
	h := &NotificationsHandler{dismissStore: store}
	rec := httptest.NewRecorder()
	h.HandleDismiss(rec, dismissRequest("d1"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d (%s), want 204", rec.Code, rec.Body.String())
	}
	if store.seenProfile != "profile-a" || len(store.dismissed) != 1 || store.dismissed[0] != "d1" {
		t.Fatalf("dismiss not scoped to the bound profile: %+v", store)
	}
}

func TestHandleDismissIdempotentAndNotFound(t *testing.T) {
	h := &NotificationsHandler{dismissStore: &fakeDismissStore{exists: true}}
	rec := httptest.NewRecorder()
	h.HandleDismiss(rec, dismissRequest("d1"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("already dismissed: status = %d, want 204", rec.Code)
	}

	h = &NotificationsHandler{dismissStore: &fakeDismissStore{}}
	rec = httptest.NewRecorder()
	h.HandleDismiss(rec, dismissRequest("missing"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: status = %d, want 404", rec.Code)
	}
}

func TestHandleDismissRefusesNonDismissible(t *testing.T) {
	h := &NotificationsHandler{dismissStore: &fakeDismissStore{err: notifications.ErrDeliveryNotDismissible}}
	rec := httptest.NewRecorder()
	h.HandleDismiss(rec, dismissRequest("critical"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("critical alert: status = %d (%s), want 409", rec.Code, rec.Body.String())
	}
}

func TestParseIncludeDismissed(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "0": false, "1": true, "true": true, "yes": false} {
		r := httptest.NewRequest(http.MethodGet, "/notifications?include_dismissed="+raw, nil)
		if got := parseIncludeDismissed(r); got != want {
			t.Errorf("include_dismissed=%q: got %v, want %v", raw, got, want)
		}
	}
}

func TestNotificationsCapabilityAdvertisesAnnouncements(t *testing.T) {
	settings := &fakeServerSettingsStore{values: map[string]string{}}
	h := NewNotificationsHandler(&notifications.System{Settings: notifications.NewSettings(settings)}, nil)
	rec := httptest.NewRecorder()
	h.HandleCapability(rec, downloadTestRequest(http.MethodGet, "/notifications/capability", nil, 7, "profile-a", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var got struct {
		Announcements  bool     `json:"announcements"`
		SupportedTypes []string `json:"supported_types"`
		Dismiss        bool     `json:"dismiss"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Announcements || !got.Dismiss {
		t.Fatalf("capability flags missing: %s", rec.Body.String())
	}
	found := map[string]bool{}
	for _, typ := range got.SupportedTypes {
		found[typ] = true
	}
	for _, want := range []string{notifications.DeliveryTypeSystemAlert, notifications.DeliveryTypeSystemAnnouncement, notifications.DeliveryTypeEpisodeAvailable} {
		if !found[want] {
			t.Errorf("supported_types missing %s: %v", want, got.SupportedTypes)
		}
	}
}
