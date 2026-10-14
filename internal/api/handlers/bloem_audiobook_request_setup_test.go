package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
)

type audiobookSetupStub struct {
	calls  int
	id     string
	v      bookrequests.Viewer
	update AudiobookRequestConnectionUpdate
}

func (s *audiobookSetupStub) List(context.Context, bookrequests.Viewer) (AudiobookRequestSetup, error) {
	s.calls++
	return AudiobookRequestSetup{Available: true}, nil
}
func (s *audiobookSetupStub) Create(context.Context, bookrequests.Viewer, AudiobookRequestConnectionInput) (bookrequests.AudiobookIntegrationSummary, error) {
	s.calls++
	return bookrequests.AudiobookIntegrationSummary{}, nil
}
func (s *audiobookSetupStub) Update(_ context.Context, v bookrequests.Viewer, id string, in AudiobookRequestConnectionUpdate) (bookrequests.AudiobookIntegrationSummary, error) {
	s.calls++
	s.v, s.id, s.update = v, id, in
	return bookrequests.AudiobookIntegrationSummary{ID: id, Name: in.Name, DownloadRoots: []string{}}, nil
}
func (s *audiobookSetupStub) SetLibrary(context.Context, bookrequests.Viewer, string, int, []string) (bookrequests.AudiobookIntegrationSummary, error) {
	s.calls++
	return bookrequests.AudiobookIntegrationSummary{}, nil
}
func (s *audiobookSetupStub) Test(_ context.Context, v bookrequests.Viewer, id string) (RequestIntegrationTestResult, error) {
	s.calls++
	s.v, s.id = v, id
	return RequestIntegrationTestResult{OK: false, Message: "One or more connection checks failed."}, nil
}

func audiobookSetupRequest(method, body, authority string) *http.Request {
	r := ebookSetupRequest(method, body, authority, true)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "200456")
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeCtx))
}

func TestBloemAudiobookConnectionEditAndTestRequirePlatformAuthority(t *testing.T) {
	s := &audiobookSetupStub{}
	h := NewBloemBookRequestsHandler(&bookRequestsServiceStub{}, nil)
	h.SetAudiobookRequestSetup(s)
	for _, handle := range []http.HandlerFunc{h.HandleAudiobookIntegrationUpdate, h.HandleAudiobookIntegrationTest} {
		w := httptest.NewRecorder()
		handle(w, audiobookSetupRequest(http.MethodPut, `{"name":"x"}`, "organization_admin"))
		if w.Code != http.StatusForbidden || s.calls != 0 {
			t.Fatalf("organization admin reached audiobook setup: %d %s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.HandleAudiobookIntegrationUpdate(w, audiobookSetupRequest(http.MethodPut, `{"name":"Rotated","plugin_config":{"qbittorrent_url":"https://client.example.test"},"credentials":{"qbittorrent_password":"synthetic-pass"}}`, "platform_admin"))
	if w.Code != http.StatusOK || s.id != "200456" || s.update.Credentials["qbittorrent_password"] != "synthetic-pass" || s.v.UserID != 7 || !s.v.Admin || strings.Contains(w.Body.String(), "synthetic-pass") {
		t.Fatalf("update: %d %s %+v", w.Code, w.Body.String(), s)
	}
	for _, body := range []string{`{"supported_media_types":["movie"]}`, `{"api_key_ref":"abk1.e30"}`, `{"name":"x"}{}`} {
		before := s.calls
		w = httptest.NewRecorder()
		h.HandleAudiobookIntegrationUpdate(w, audiobookSetupRequest(http.MethodPut, body, "platform_admin"))
		if w.Code != http.StatusBadRequest || s.calls != before {
			t.Fatalf("unsupported update field reached setup: %d %s", w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	h.HandleAudiobookIntegrationTest(w, audiobookSetupRequest(http.MethodPost, "", "platform_admin"))
	if w.Code != http.StatusOK || s.id != "200456" || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("test: %d %s", w.Code, w.Body.String())
	}
}
