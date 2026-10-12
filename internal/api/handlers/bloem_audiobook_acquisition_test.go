package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/go-chi/chi/v5"
)

type acquisitionStub struct {
	calls  int
	viewer bookrequests.Viewer
}

func (s *acquisitionStub) AudiobookAcquisition(_ context.Context, v bookrequests.Viewer, _ string) (bookrequests.AudiobookAcquisition, error) {
	s.calls++
	s.viewer = v
	return bookrequests.AudiobookAcquisition{}, nil
}
func (s *acquisitionStub) ListAudiobookAcquisitions(_ context.Context, v bookrequests.Viewer, _, _ string, _ int) (bookrequests.AudiobookAcquisitionList, error) {
	s.calls++
	s.viewer = v
	return bookrequests.AudiobookAcquisitionList{Acquisitions: []bookrequests.AudiobookAcquisition{}}, nil
}
func (s *acquisitionStub) ApproveAudiobookCandidate(context.Context, bookrequests.Viewer, string, bookrequests.AudiobookCandidateApproval) (bookrequests.Request, error) {
	s.calls++
	return bookrequests.Request{}, nil
}
func (s *acquisitionStub) AcceptAudiobookDownload(context.Context, bookrequests.Viewer, string, bookrequests.ActionInput) (bookrequests.Request, error) {
	s.calls++
	return bookrequests.Request{}, nil
}
func TestAudiobookAcquisitionsRequireBoundAdminContext(t *testing.T) {
	s := &acquisitionStub{}
	h := NewBloemBookRequestsHandler(&bookRequestsServiceStub{}, nil)
	h.SetAudiobookAcquisition(s)
	w := httptest.NewRecorder()
	h.HandleAudiobookAcquisitionList(w, httptest.NewRequest(http.MethodGet, "/organization/book-requests/acquisitions", nil))
	if w.Code == http.StatusOK || s.calls != 0 {
		t.Fatalf("unbound admin reached acquisition: %d", w.Code)
	}
	r := ebookSetupRequest(http.MethodGet, "", "organization_admin", true)
	w = httptest.NewRecorder()
	h.HandleAudiobookAcquisitionList(w, r)
	if w.Code != http.StatusOK || s.calls != 1 || !s.viewer.Admin || !strings.Contains(w.Body.String(), `"acquisitions":[]`) {
		t.Fatalf("bound admin: %d %s", w.Code, w.Body.String())
	}
	router := chi.NewRouter()
	router.Get("/{id}", h.HandleAudiobookAcquisition)
	w = httptest.NewRecorder()
	r = ebookSetupRequest(http.MethodGet, "", "organization_admin", true)
	r.URL.Path = "/invalid"
	router.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || s.calls != 1 {
		t.Fatalf("invalid id reached service %d", w.Code)
	}
}
