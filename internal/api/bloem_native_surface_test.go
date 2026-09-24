package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// An ebook read is a binary container (EPUB, PDF, CBZ) that is already
// compressed. The v1 route is excluded from gzip; the native mount serves the
// same bytes and must be excluded too. The prefix carries an extra path
// segment, so the exclusion has to know about it -- without that it silently
// re-compresses every ebook download.
func TestNativeEbookReadSkipsCompressionLikeV1(t *testing.T) {
	for _, path := range []string{
		"/api/v1/ebooks/c1/files/f1/read",
		"/api/v2/ebooks/c1/files/f1/read",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if !skipNativeMediaCompression(req) {
			t.Errorf("%s: expected compression to be skipped", path)
		}
	}
}

// Nothing on the native surface is compression-excluded: that switch matches
// stream, playback/transcode, downloads/file, direct-download and ebooks/read,
// and /api/bloem/v1 serves none of them. A prefix normalisation once existed
// here for a native ebook mount that turned out to be unnecessary -- ebooks was
// already on /api/v2 -- and this pins the surface's actual behavior so the
// normalisation is not reintroduced without a route that needs it.
func TestNativeNonMediaRoutesStillCompress(t *testing.T) {
	for _, path := range []string{
		"/api/bloem/v1/capabilities",
		"/api/bloem/v1/organizations",
		"/api/v2/ebooks/c1/progress",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if skipNativeMediaCompression(req) {
			t.Errorf("%s: compression must not be skipped for a non-media route", path)
		}
	}
}

// Live TV is Bloem's own feature and will never appear on /api/v2, so the
// native surface is its permanent home. Both prefixes call one shared
// mountLiveTVRoutes, and this asserts the native mount really registers the
// viewer, streaming and admin routes rather than silently registering nothing.
func TestBloemClientSurfaceMountsLiveTV(t *testing.T) {
	surface := bloemClientSurface{
		auth:        apimw.NewAuthMiddleware(nil, nil, nil, nil),
		liveTV:      &handlers.LiveTVHandler{},
		liveTVAdmin: func(next http.Handler) http.Handler { return next },
	}
	r := chi.NewRouter()
	surface.mount(r)

	var mounted []string
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		mounted = append(mounted, method+" "+route)
		return nil
	})
	joined := strings.Join(mounted, "\n")
	for _, want := range []string{
		"/livetv/capability",                   // probe
		"/livetv/channels",                     // viewer
		"/livetv/guide",                        //
		"/livetv/channels/{channelId}/session", // session lifecycle
		"/livetv/sessions/{sessionId}/stream",  // streaming
		"/livetv/live-hls/{playbackId}/{name}", //
		"/livetv/recordings",                   // DVR
		"/livetv/series-rules",                 //
		"/livetv/tuners",                       // admin
		"/livetv/guide-sources",                //
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("Live TV route %q not mounted on the native surface", want)
		}
	}
}

// A surface built without acting-admin middleware must not panic (chi's r.Use
// dereferences it) and must not fall open: the routes it guards add tuners and
// rewrite guide sources.
func TestLiveTVAdminFallbackDeniesRatherThanPanicking(t *testing.T) {
	surface := bloemClientSurface{
		auth:   apimw.NewAuthMiddleware(nil, nil, nil, nil),
		liveTV: &handlers.LiveTVHandler{},
		// liveTVAdmin deliberately nil
	}
	r := chi.NewRouter()
	surface.mount(r) // must not panic

	rec := httptest.NewRecorder()
	denyLiveTVAdmin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("request reached the guarded handler; the fallback must deny")
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/livetv/tuners", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rec.Body.String(), "admin_unavailable") {
		t.Errorf("body = %q, want an admin_unavailable error", rec.Body.String())
	}
}
