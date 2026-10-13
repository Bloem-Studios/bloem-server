package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
)

func TestLiveTVBrowseOnlyDeliveryBoundary(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	for _, allowed := range []bool{true, false} {
		for _, method := range []string{"GET", "HEAD"} {
			r := httptest.NewRequest(method, "/livetv/stream?st=previous-ticket", nil)
			r = r.WithContext(access.SetScope(r.Context(), access.Scope{LiveTVAllowed: true, PlaybackAllowed: allowed}))
			w := httptest.NewRecorder()
			RequireLiveTVAccess(next).ServeHTTP(w, r)
			if w.Code != 204 {
				t.Fatal("browse denied", w.Code)
			}
			w = httptest.NewRecorder()
			RequireLiveTVStreamAccess(next).ServeHTTP(w, r)
			want := 403
			if allowed {
				want = 204
			}
			if w.Code != want {
				t.Fatalf("playback=%v method=%s delivery=%d", allowed, method, w.Code)
			}
		}
	}
}
