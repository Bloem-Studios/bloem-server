package handlers

import (
	"context"
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

func (f *fakeDismissStore) Dismiss(_ context.Context, _ int, profileID, id string) (bool, error) {
	f.seenProfile = profileID
	f.dismissed = append(f.dismissed, id)
	return f.transition, f.err
}

func (f *fakeDismissStore) Exists(context.Context, int, string, string) (bool, error) {
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
