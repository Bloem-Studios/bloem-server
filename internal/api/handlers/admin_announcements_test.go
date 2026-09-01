package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type fakeAnnouncementService struct {
	created     []notifications.AnnouncementInput
	createdBy   int
	createErr   error
	listed      []notifications.Announcement
	withdrawn   []string
	withdrawErr error
}

func (f *fakeAnnouncementService) Create(_ context.Context, createdBy int, in notifications.AnnouncementInput) (*notifications.Announcement, error) {
	f.createdBy = createdBy
	f.created = append(f.created, in)
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &notifications.Announcement{ID: "ann-1", Type: in.Type, Body: in.Body, Targeting: in.Targeting, RecipientCount: 3}, nil
}

func (f *fakeAnnouncementService) List(context.Context) ([]notifications.Announcement, error) {
	return f.listed, nil
}

func (f *fakeAnnouncementService) Withdraw(_ context.Context, id string) error {
	f.withdrawn = append(f.withdrawn, id)
	return f.withdrawErr
}

func TestAdminAnnouncementsCreateReturnsCreatedWithRecipientCount(t *testing.T) {
	svc := &fakeAnnouncementService{}
	h := &AdminAnnouncementsHandler{service: svc}
	// This request body is the client contract quoted in
	// docs/specs/client-engagement.md "Implementation notes (S-1)".
	body := `{"type":"system.alert","body":{"title":"Maintenance","body":"Tonight","severity":"warning","deeplink":"bloem://settings/status","image_url":"https://cdn.example/banner.png","dismissible":true,"cta":{"label":"Status","url":"https://status.example"},"expires_at":"2030-01-01T00:00:00Z"},"targeting":{"audience":"role","role":"admin"}}`
	rec := httptest.NewRecorder()
	h.HandleCreate(rec, downloadTestRequest(http.MethodPost, "/admin/notifications/announcements", []byte(body), 42, "", ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (%s), want 201", rec.Code, rec.Body.String())
	}
	if svc.createdBy != 42 || len(svc.created) != 1 || svc.created[0].Targeting.Role != "admin" {
		t.Fatalf("service call not recorded: by=%d created=%+v", svc.createdBy, svc.created)
	}
	var got notifications.Announcement
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "ann-1" || got.RecipientCount != 3 || got.Body.Deeplink != "bloem://settings/status" || got.Body.CTA == nil {
		t.Fatalf("unexpected response: %+v", got)
	}
	if !strings.HasPrefix(rec.Body.String(), `{"id":"ann-1","type":"system.alert","body":{"title":"Maintenance"`) {
		t.Fatalf("response must carry a top-level id first: %s", rec.Body.String())
	}
}

func TestAdminAnnouncementsErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
		code string
	}{
		{notifications.ErrAnnouncementNoRecipients, http.StatusUnprocessableEntity, "no_recipients"},
		{errors.Join(notifications.ErrAlertBodyInvalid, errors.New("title is required")), http.StatusBadRequest, "bad_request"},
		{notifications.ErrAnnouncementInvalid, http.StatusBadRequest, "bad_request"},
		{errors.New("boom"), http.StatusInternalServerError, "internal_error"},
	}
	for _, tc := range cases {
		h := &AdminAnnouncementsHandler{service: &fakeAnnouncementService{createErr: tc.err}}
		rec := httptest.NewRecorder()
		h.HandleCreate(rec, downloadTestRequest(http.MethodPost, "/admin/notifications/announcements", []byte(`{"body":{"title":"x"}}`), 1, "", ""))
		if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.code) {
			t.Errorf("err %v: status=%d body=%s, want %d/%s", tc.err, rec.Code, rec.Body.String(), tc.want, tc.code)
		}
	}
	rec := httptest.NewRecorder()
	(&AdminAnnouncementsHandler{service: &fakeAnnouncementService{}}).HandleCreate(rec,
		downloadTestRequest(http.MethodPost, "/admin/notifications/announcements", []byte(`{not json`), 1, "", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: status = %d, want 400", rec.Code)
	}
}

func TestAdminAnnouncementsListAndWithdraw(t *testing.T) {
	svc := &fakeAnnouncementService{listed: []notifications.Announcement{{ID: "a1", Type: notifications.DeliveryTypeSystemAnnouncement}}}
	h := &AdminAnnouncementsHandler{service: svc}

	rec := httptest.NewRecorder()
	h.HandleList(rec, downloadTestRequest(http.MethodGet, "/admin/notifications/announcements", nil, 1, "", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"announcements":[{"id":"a1"`) {
		t.Fatalf("list: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleDelete(rec, withChiID(downloadTestRequest(http.MethodDelete, "/admin/notifications/announcements/a1", nil, 1, "", ""), "a1"))
	if rec.Code != http.StatusNoContent || len(svc.withdrawn) != 1 || svc.withdrawn[0] != "a1" {
		t.Fatalf("withdraw: status=%d withdrawn=%v", rec.Code, svc.withdrawn)
	}

	svc.withdrawErr = notifications.ErrAnnouncementNotFound
	rec = httptest.NewRecorder()
	h.HandleDelete(rec, withChiID(downloadTestRequest(http.MethodDelete, "/admin/notifications/announcements/zz", nil, 1, "", ""), "zz"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("withdraw unknown: status=%d, want 404", rec.Code)
	}

	// An empty list serializes as [] rather than null.
	rec = httptest.NewRecorder()
	(&AdminAnnouncementsHandler{service: &fakeAnnouncementService{}}).HandleList(rec,
		downloadTestRequest(http.MethodGet, "/admin/notifications/announcements", nil, 1, "", ""))
	if !strings.Contains(rec.Body.String(), `"announcements":[]`) {
		t.Fatalf("empty list body = %s", rec.Body.String())
	}
}

func TestAdminAnnouncementsUnavailableWithoutService(t *testing.T) {
	h := NewAdminAnnouncementsHandler(nil)
	rec := httptest.NewRecorder()
	h.HandleList(rec, downloadTestRequest(http.MethodGet, "/admin/notifications/announcements", nil, 1, "", ""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
