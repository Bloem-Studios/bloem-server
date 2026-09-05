package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/go-chi/chi/v5"
)

func TestLiveTVRoutesRequireSeparateGrant(t *testing.T) {
	router := chi.NewRouter()
	mountLiveTVRoutes(router, handlers.NewLiveTVHandler(livetv.NewService(nil)), apimw.RequireAdmin)
	for _, route := range []struct{ method, path string }{
		{"GET", "/livetv/channels"}, {"GET", "/livetv/guide"}, {"GET", "/livetv/programs/p"},
		{"GET", "/livetv/recordings"}, {"GET", "/livetv/series-rules"}, {"POST", "/livetv/channels/c/session"},
		{"POST", "/livetv/sessions/s/heartbeat"}, {"POST", "/livetv/recordings"}, {"POST", "/livetv/series-rules"},
		{"DELETE", "/livetv/recordings/r"}, {"DELETE", "/livetv/series-rules/r"},
		{"GET", "/livetv/sessions/s/stream"}, {"HEAD", "/livetv/sessions/s/stream"}, {"GET", "/livetv/live-hls/p/index.m3u8"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, nil)
			req.Header.Set("X-Profile-Id", "profile")
			req = req.WithContext(access.SetScope(apimw.SetProfileID(req.Context(), "profile"), access.Scope{PlaybackAllowed: true}))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLiveTVCapabilityMountsOnNativeSurface(t *testing.T) {
	router := chi.NewRouter()
	surface := bloemClientSurface{auth: &apimw.AuthMiddleware{}, liveTV: handlers.NewLiveTVHandler(livetv.NewService(nil))}
	surface.mount(router)
	found := false
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == "GET" && route == "/livetv/capability" {
			found = true
		}
		return nil
	})
	if err != nil || !found {
		t.Fatalf("native capability missing: %v", err)
	}
}
