package api

import (
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/config"
)

// The normaliser is only useful if it is actually on the chain every native
// request passes through. Re-declaring the middleware stack in a test would let
// the two drift, so this drives the real useBaseMiddleware, the same way the
// socket and h2 conformance tests do.
func TestBaseMiddlewareFoldsBloemClientHeadersOntoTheCanonicalNames(t *testing.T) {
	cfg, err := config.LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}

	var seen http.Header
	root := chi.NewRouter()
	useBaseMiddleware(root, Dependencies{Config: cfg})
	root.Get("/api/v1/probe", func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil)
	req.Header.Set("X-Bloem-Client-Family", "tv")
	req.Header.Set("X-Bloem-Device-Id", "living-room-tv")
	req.Header.Set("X-Bloem-Device-Name", "Living Room")
	req.Header.Set("X-Bloem-Device-Platform", "androidtv")
	root.ServeHTTP(httptest.NewRecorder(), req)

	for canonical, want := range map[string]string{
		"X-Silo-Client-Family":   "tv",
		"X-Silo-Device-Id":       "living-room-tv",
		"X-Silo-Device-Name":     "Living Room",
		"X-Silo-Device-Platform": "androidtv",
	} {
		if got := seen.Get(canonical); got != want {
			t.Fatalf("handler saw %s = %q, want %q — the normaliser is not on the base chain",
				canonical, got, want)
		}
	}

	// And an upstream-compatible client is unaffected by the same chain.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/probe", nil)
	req.Header.Set("X-Silo-Client-Family", "desktop")
	root.ServeHTTP(httptest.NewRecorder(), req)
	if got := seen.Get("X-Silo-Client-Family"); got != "desktop" {
		t.Fatalf("X-Silo-Client-Family = %q, want %q", got, "desktop")
	}
	if _, present := seen[textproto.CanonicalMIMEHeaderKey("X-Bloem-Client-Family")]; present {
		t.Fatal("a Bloem header was invented for an upstream-compat request")
	}
}
