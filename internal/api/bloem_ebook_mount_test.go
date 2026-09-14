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

// The ebook surface is on loan from the v1 tree until Silo publishes ebooks on
// /api/v2. This asserts the loan is actually wired: a compile-clean mount that
// registers nothing would be indistinguishable from success.
func TestBloemClientSurfaceMountsEbooksWhenPresent(t *testing.T) {
	// The authenticated group is skipped entirely without auth middleware,
	// so the surface needs one before any of its routes exist. Validators are
	// nil: this asserts what is registered, never what it answers.
	surface := bloemClientSurface{
		auth:   apimw.NewAuthMiddleware(nil, nil, nil, nil),
		ebooks: &handlers.EbookReaderHandler{},
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
		"/ebooks/capability",
		"/ebooks/{content_id}/files/{file_id}/read",
		"/ebooks/{content_id}/progress",
		"/ebooks/{content_id}/reader-config",
		"/ebooks/{content_id}/annotations",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("route %q not mounted; got:\n%s", want, joined)
		}
	}
}

// Without the handler the routes must be absent rather than mounted onto a nil
// receiver, which would answer 200-shaped panics instead of 404.
func TestBloemClientSurfaceOmitsEbooksWhenAbsent(t *testing.T) {
	r := chi.NewRouter()
	bloemClientSurface{}.mount(r)
	_ = chi.Walk(r, func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.Contains(route, "ebooks") {
			t.Errorf("ebook route %q mounted without a handler", route)
		}
		return nil
	})
}

// An ebook read is a binary container (EPUB, PDF, CBZ) that is already
// compressed. The v1 route is excluded from gzip; the native mount serves the
// same bytes and must be excluded too. The prefix carries an extra path
// segment, so the exclusion has to know about it -- without that it silently
// re-compresses every ebook download.
func TestNativeEbookReadSkipsCompressionLikeV1(t *testing.T) {
	for _, path := range []string{
		"/api/v1/ebooks/c1/files/f1/read",
		"/api/bloem/v1/ebooks/c1/files/f1/read",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if !skipNativeMediaCompression(req) {
			t.Errorf("%s: expected compression to be skipped", path)
		}
	}
}

// The normalisation must not make unrelated native routes look like media.
func TestNativeNonMediaRoutesStillCompress(t *testing.T) {
	for _, path := range []string{
		"/api/bloem/v1/capabilities",
		"/api/bloem/v1/ebooks/c1/progress",
		"/api/bloem/v1/organizations",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if skipNativeMediaCompression(req) {
			t.Errorf("%s: compression must not be skipped for a non-media route", path)
		}
	}
}
