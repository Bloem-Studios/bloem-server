package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/listcollections"
)

type listHTTPBackendFixture struct {
	calls int
	err   error
}

func (b *listHTTPBackendFixture) Connections(context.Context, int, string) ([]BloemListConnection, error) {
	b.calls++
	return []BloemListConnection{{InstallationID: 1, Origin: "https://pastime.example"}}, b.err
}
func (b *listHTTPBackendFixture) Subject(context.Context, int, string, int, string, string) (listcollections.Subject, error) {
	b.calls++
	return listcollections.Subject{}, listcollections.ErrAuthority
}
func (b *listHTTPBackendFixture) ValidateURL(string) error { return nil }
func TestBloemListCapabilitiesRequireViewerAndExposeNoCredentials(t *testing.T) {
	backend := &listHTTPBackendFixture{}
	h := &BloemListCollectionsHandler{Service: &listcollections.Service{}, Backend: backend}
	r := httptest.NewRequest("GET", "/list-collections/capabilities", nil)
	w := httptest.NewRecorder()
	h.HandleCapabilities(w, r)
	if w.Code != 401 || backend.calls != 0 {
		t.Fatal(w.Code, backend.calls)
	}
	r = r.WithContext(seasonalViewerContext(uuid.New()))
	w = httptest.NewRecorder()
	h.HandleCapabilities(w, r)
	if w.Code != 200 || backend.calls != 1 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "credential") || strings.Contains(w.Body.String(), "subject") {
		t.Fatal("private authority leaked")
	}
	h.Service = nil
	w = httptest.NewRecorder()
	h.HandleCapabilities(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestBloemListPreviewRejectsForgedAuthorityAndTrailingBodies(t *testing.T) {
	for _, body := range []string{`{"installation_id":1,"url":"https://pastime.example/lists/1","user_id":2}`, `{"installation_id":1,"url":"x"} {}`, `{`, strings.Repeat("x", 20000)} {
		backend := &listHTTPBackendFixture{}
		h := &BloemListCollectionsHandler{Service: &listcollections.Service{}, Backend: backend}
		r := httptest.NewRequest(http.MethodPost, "/list-collections/previews", strings.NewReader(body)).WithContext(seasonalViewerContext(uuid.New()))
		w := httptest.NewRecorder()
		h.HandlePreview(w, r)
		if w.Code != 400 || backend.calls != 0 {
			t.Fatal(w.Code, w.Body.String(), backend.calls)
		}
	}
}
func TestBloemListDomainErrorsKeepAuthenticationDistinct(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{{listcollections.ErrAuthority, 403}, {listcollections.ErrExpired, 409}, {listcollections.ErrConflict, 409}, {listcollections.ErrSnapshot, 400}, {errors.New("private database detail"), 503}} {
		w := httptest.NewRecorder()
		listCollectionError(w, tc.err)
		if w.Code != tc.code || strings.Contains(w.Body.String(), "private database") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	b := listBindingStatus(listcollections.Binding{ID: "id", Subject: listcollections.Subject{UserID: 42}, SigningKey: "private-key"}, listcollections.RequestCounts{Quota: 1})
	raw, err := json.Marshal(b)
	if err != nil || strings.Contains(string(raw), "private-key") || strings.Contains(string(raw), "UserID") {
		t.Fatal(string(raw), err)
	}
	if !strings.Contains(string(raw), `"unmatched":[]`) {
		t.Fatal("null list", string(raw))
	}
}
