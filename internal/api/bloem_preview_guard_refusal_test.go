package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// problemOf decodes a v2 problem answer and reports its type id, or "" when
// the answer is not one.
func problemOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Header().Get("Content-Type") != "application/problem+json" {
		return ""
	}
	var p struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Status != w.Code {
		t.Fatalf("problem body %q: %v", w.Body, err)
	}
	if p.Code != "" {
		t.Fatalf("Bloem's code %q is in the problem body; it belongs in X-Bloem-Denial", p.Code)
	}
	return p.Type[strings.LastIndex(p.Type, "/")+1:]
}

// The preview guards run ahead of routing, so Silo's gate translation never
// sees their refusals. On a Silo /api/v2 route they must still answer as
// Silo does, with a problem type a Silo app knows; Bloem's own routes keep
// the guard's JSON.
func TestPreviewHostGuardRefusesSiloRoutesWithProblems(t *testing.T) {
	settings := hostSettings{auth.PreviewGuestSettings{Enabled: true, Host: "browse.example.test", AccountID: 7}}
	tokens := hostTokens{
		"guest":  {UserID: 7, AuthMethod: auth.AuthMethodPreviewGuest, TokenType: auth.TokenTypeAccess},
		"member": {UserID: 8, TokenType: auth.TokenTypeAccess},
	}
	serve := func(guard func(http.Handler) http.Handler, host, method, path, bearer string) *httptest.ResponseRecorder {
		h := guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		r := httptest.NewRequest(method, path, nil)
		r.Host = host
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	guard := previewHostGuard(settings, tokens)
	for _, tc := range []struct {
		name, host, method, path, bearer string
		status                           int
		problem, denial                  string
	}{
		{"sign-in on the preview host", "browse.example.test", "POST", "/api/v2/auth/login", "", http.StatusForbidden, "permission_denied", "preview_host"},
		{"account credential on the preview host", "browse.example.test", "GET", "/api/v2/catalog/items/x", "member", http.StatusUnauthorized, "invalid_token", "preview_host"},
		{"guest credential on another host", "lib.example.test", "GET", "/api/v2/catalog/items/x", "guest", http.StatusUnauthorized, "invalid_token", "preview_session_invalid"},
	} {
		w := serve(guard, tc.host, tc.method, tc.path, tc.bearer)
		if w.Code != tc.status || problemOf(t, w) != tc.problem || w.Header().Get("X-Bloem-Denial") != tc.denial {
			t.Errorf("%s: %d %s %q denial=%q, want %d %s denial=%q", tc.name, w.Code, w.Header().Get("Content-Type"), w.Body, w.Header().Get("X-Bloem-Denial"), tc.status, tc.problem, tc.denial)
		}
	}

	unavailable := serve(previewHostGuard(failingHostSettings{}, tokens), "lib.example.test", "POST", "/api/v2/auth/login", "")
	if unavailable.Code != http.StatusServiceUnavailable || problemOf(t, unavailable) != "dependency_unavailable" || unavailable.Header().Get("Retry-After") == "" {
		t.Errorf("unreadable settings: %d %q Retry-After=%q, want 503 dependency_unavailable with Retry-After", unavailable.Code, unavailable.Body, unavailable.Header().Get("Retry-After"))
	}

	busy := previewHostGuard(settings, tokens)
	var limited *httptest.ResponseRecorder
	for range 400 {
		if w := serve(busy, "browse.example.test", "GET", "/api/v2/catalog/items/x", "guest"); w.Code == http.StatusTooManyRequests {
			limited = w
			break
		}
	}
	if limited == nil || problemOf(t, limited) != "rate_limited" || limited.Header().Get("Retry-After") == "" || limited.Header().Get("X-Bloem-Denial") != "preview_busy" {
		t.Errorf("busy preview: %+v, want 429 rate_limited with Retry-After and denial preview_busy", limited)
	}

	// Bloem's own routes keep the guard's JSON.
	w := serve(guard, "lib.example.test", "GET", "/api/bloem/v2/preview/events", "guest")
	if w.Code != http.StatusUnauthorized || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Body.String(), `"code":"preview_session_invalid"`) {
		t.Errorf("Bloem route: %d %s %q, want the guard's JSON", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
}

// The shared preview account's password sign-in is refused before the login
// handler runs, as a Silo refusal. Everything else reaches Silo's handler
// with the body it was sent, whatever its size: Silo's own body limit (1 MiB,
// 413) and validation answer it, not a plain-text 400 at 16 KiB.
func TestPreviewAccountLoginGuardAnswersAsSilo(t *testing.T) {
	var lookupErr error
	guard := previewAccountLoginGuardWith(func(_ context.Context, username string) (bool, error) {
		return username == "preview-shared", lookupErr
	})
	serve := func(body string) (*httptest.ResponseRecorder, int) {
		received := -1
		h := guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			received = len(raw)
			w.WriteHeader(http.StatusNoContent)
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(body)))
		return w, received
	}

	w, _ := serve(`{"username":"preview-shared","password":"synthetic"}`)
	if w.Code != http.StatusForbidden || problemOf(t, w) != "permission_denied" || w.Header().Get("X-Bloem-Denial") != "preview_account" {
		t.Errorf("shared account: %d %q denial=%q, want 403 permission_denied denial=preview_account", w.Code, w.Body, w.Header().Get("X-Bloem-Denial"))
	}
	for name, body := range map[string]string{
		"20 KiB of whitespace": `{"username":"someone",` + strings.Repeat(" ", 20<<10) + `"password":"synthetic"}`,
		"over 1 MiB":           `{"username":"someone","password":"` + strings.Repeat("y", 1100<<10) + `"}`,
	} {
		w, received := serve(body)
		if w.Code != http.StatusNoContent || received != len(body) {
			t.Errorf("%s: %d, handler read %d of %d bytes; want it passed on whole", name, w.Code, received, len(body))
		}
	}

	lookupErr = errors.New("database unreachable")
	w, _ = serve(`{"username":"someone","password":"synthetic"}`)
	if w.Code != http.StatusServiceUnavailable || problemOf(t, w) != "dependency_unavailable" || w.Header().Get("Retry-After") == "" {
		t.Errorf("lookup failure: %d %q, want 503 dependency_unavailable with Retry-After", w.Code, w.Body)
	}
}
