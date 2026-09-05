package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
)

func TestRequireLiveTVAccess(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/api/v1/livetv/channels", nil)
		req = req.WithContext(access.SetScope(req.Context(), access.Scope{LiveTVAllowed: allowed, PlaybackAllowed: true}))
		rec := httptest.NewRecorder()
		RequireLiveTVAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(rec, req)
		want := 403
		if allowed {
			want = 204
		}
		if rec.Code != want {
			t.Fatalf("allowed=%v status=%d", allowed, rec.Code)
		}
	}
	req := httptest.NewRequest("GET", "/api/v1/livetv/channels", nil)
	rec := httptest.NewRecorder()
	RequireLiveTVAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("unresolved scope allowed: %d", rec.Code)
	}
}
