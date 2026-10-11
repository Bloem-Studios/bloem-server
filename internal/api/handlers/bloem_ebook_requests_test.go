package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/google/uuid"
)

type ebookRequestsServiceStub struct{ bookRequestsServiceStub }

func (s *ebookRequestsServiceStub) CapabilityEbook(ctx context.Context, v bookrequests.Viewer) (bookrequests.Capability, error) {
	return s.Capability(ctx, v)
}
func (s *ebookRequestsServiceStub) ListEbooks(ctx context.Context, v bookrequests.Viewer, limit, offset int) ([]bookrequests.Request, error) {
	return s.List(ctx, v, limit, offset)
}
func (s *ebookRequestsServiceStub) GetEbook(ctx context.Context, v bookrequests.Viewer, id string) (bookrequests.Request, error) {
	return s.Get(ctx, v, id)
}
func (s *ebookRequestsServiceStub) CreateEbook(ctx context.Context, v bookrequests.Viewer, in bookrequests.EbookCreateInput, key string) (bookrequests.Request, bool, error) {
	return s.Create(ctx, v, bookrequests.CreateInput{Title: in.Title, Author: in.Author, ISBN: in.ISBN, Note: in.Note}, key)
}
func (s *ebookRequestsServiceStub) ActEbook(ctx context.Context, v bookrequests.Viewer, id, action string, in bookrequests.ActionInput) (bookrequests.Request, error) {
	return s.Act(ctx, v, id, action, in)
}

func TestBloemEbookRequestCreateRejectsAudiobookAndAuthorityFields(t *testing.T) {
	org := uuid.New()
	for _, body := range []string{
		`{"title":"Book","isbn":"9780306406157","asin":""}`,
		`{"title":"Book","isbn":"9780306406157","media_type":"audiobook"}`,
		`{"title":"Book","isbn":"9780306406157","organization_id":"foreign"}`,
		`{"title":"Book","isbn":"9780306406157"}{"title":"Second"}`,
	} {
		s := &ebookRequestsServiceStub{}
		h := NewBloemBookRequestsHandler(&bookRequestsServiceStub{}, bloemSeasonalProfilesStub{org: org})
		h.SetEbookRequests(s)
		r := httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/ebook-requests", strings.NewReader(body)).WithContext(seasonalViewerContext(org))
		w := httptest.NewRecorder()
		h.EbookRequests().HandleCreate(w, r)
		if w.Code != http.StatusBadRequest || s.calls != 0 {
			t.Fatalf("unexpected response/calls: %d %s %d", w.Code, w.Body.String(), s.calls)
		}
	}
}

func TestBloemEbookRequestUsesBoundViewerAndRetryKey(t *testing.T) {
	org := uuid.New()
	s := &ebookRequestsServiceStub{bookRequestsServiceStub: bookRequestsServiceStub{created: true}}
	h := NewBloemBookRequestsHandler(&bookRequestsServiceStub{}, bloemSeasonalProfilesStub{org: org})
	h.SetEbookRequests(s)
	r := httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/ebook-requests", strings.NewReader(`{"title":"Book","author":"Author","isbn":"9780306406157","note":"Please"}`)).WithContext(seasonalViewerContext(org))
	r.Header.Set("Idempotency-Key", "stable-ebook-request-key")
	w := httptest.NewRecorder()
	h.EbookRequests().HandleCreate(w, r)
	if w.Code != http.StatusCreated || s.calls != 1 || s.viewer.OrganizationID != org || s.viewer.UserID != 7 || s.viewer.ProfileID != "viewer" || s.key != "stable-ebook-request-key" || s.in.ISBN != "9780306406157" {
		t.Fatalf("unexpected response/authority: %d %s %+v", w.Code, w.Body.String(), s)
	}
}

func TestBloemEbookRequestErrorsNameTheEbookLane(t *testing.T) {
	org := uuid.New()
	s := &ebookRequestsServiceStub{bookRequestsServiceStub: bookRequestsServiceStub{err: bookrequests.ErrForbidden}}
	h := NewBloemBookRequestsHandler(&bookRequestsServiceStub{}, bloemSeasonalProfilesStub{org: org})
	h.SetEbookRequests(s)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/ebook-requests/capability", nil).WithContext(seasonalViewerContext(org))
	h.EbookRequests().HandleCapability(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Ebook") || strings.Contains(w.Body.String(), "Audiobook") {
		t.Fatalf("wrong lane error: %d %s", w.Code, w.Body.String())
	}
}
