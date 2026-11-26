package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// clientOriginRouter is a router like the API's: no OPTIONS routes, so a
// preflight that reaches routing gets the 405 Moonfin's Xbox page got.
func clientOriginRouter() http.Handler {
	r := chi.NewRouter()
	useBloemClientOrigins(r)
	r.Get("/api/v2/system/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(`{"api_major":2}`))
	})
	r.Get("/web/page", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("page")) })
	return r
}

func preflight(origin, path, headers string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodOptions, path, nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	if headers != "" {
		req.Header.Set("Access-Control-Request-Headers", headers)
	}
	rec := httptest.NewRecorder()
	clientOriginRouter().ServeHTTP(rec, req)
	return rec
}

func TestBloemClientOriginPreflightIsAllowedForMoonfinXbox(t *testing.T) {
	for _, origin := range []string{"http://moonfin.internal", "https://moonfin.internal"} {
		rec := preflight(origin, "/api/v2/system/info", "authorization, content-type, x-silo-client, x-profile-id")
		h := rec.Header()
		if rec.Code != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != origin ||
			h.Get("Access-Control-Allow-Methods") != bloemClientOriginMethods ||
			h.Get("Access-Control-Allow-Headers") != "authorization, content-type, x-silo-client, x-profile-id" ||
			h.Get("Access-Control-Max-Age") != "600" || h.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("%s: status %d headers %v", origin, rec.Code, h)
		}
	}
}

func TestBloemClientOriginPreflightFromAnyOtherOriginIsUnchanged(t *testing.T) {
	for _, origin := range []string{"https://evil.example", "http://moonfin.internal.evil.example", "null", ""} {
		rec := preflight(origin, "/api/v2/system/info", "authorization")
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("%q: status %d headers %v", origin, rec.Code, rec.Header())
		}
	}
}

func TestBloemClientOriginOnlyCoversTheNativeAPI(t *testing.T) {
	rec := preflight("http://moonfin.internal", "/web/page", "authorization")
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("status %d headers %v", rec.Code, rec.Header())
	}
	req := httptest.NewRequest(http.MethodGet, "/web/page", nil)
	req.Header.Set("Origin", "http://moonfin.internal")
	get := httptest.NewRecorder()
	clientOriginRouter().ServeHTTP(get, req)
	if get.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("a page outside /api/v2 became readable: %v", get.Header())
	}
}

func TestBloemClientOriginResponsesAreReadable(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/info", nil)
	req.Header.Set("Origin", "http://moonfin.internal")
	rec := httptest.NewRecorder()
	clientOriginRouter().ServeHTTP(rec, req)
	h := rec.Header()
	if rec.Code != http.StatusOK || rec.Body.String() != `{"api_major":2}` ||
		h.Get("Access-Control-Allow-Origin") != "http://moonfin.internal" ||
		h.Get("Access-Control-Expose-Headers") != "*" || h.Get("Vary") != "Origin" ||
		h.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("status %d headers %v", rec.Code, h)
	}

	plain := httptest.NewRecorder()
	clientOriginRouter().ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/api/v2/system/info", nil))
	if plain.Header().Get("Access-Control-Allow-Origin") != "" || plain.Header().Get("Vary") != "" {
		t.Fatalf("a request without an Origin changed: %v", plain.Header())
	}
}

func TestBloemClientOriginRequestedHeadersKeepsOnlyFieldNames(t *testing.T) {
	cases := map[string]string{
		"":                                 "",
		"Authorization":                    "Authorization",
		" authorization ,x-profile-token ": "authorization, x-profile-token",
		"authorization, bad header":        "",
		"x\r\nSet-Cookie: a=b":             "",
	}
	for in, want := range cases {
		if got := bloemClientOriginRequestedHeaders(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
	long := make([]byte, bloemClientOriginMaxHeaders+1)
	for i := range long {
		long[i] = 'a'
	}
	if got := bloemClientOriginRequestedHeaders(string(long)); got != "" {
		t.Errorf("an oversized list was echoed")
	}
}
