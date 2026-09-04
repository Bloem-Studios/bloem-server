package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// canonicalClientHeaders is the list under test, written out rather than
// derived from clientHeaderAliases so a header silently dropped from the map
// fails here instead of quietly shrinking the coverage.
var canonicalClientHeaders = []string{
	"X-Silo-Device-Id",
	"X-Silo-Device-Name",
	"X-Silo-Device-Platform",
	"X-Silo-Client-Family",
	"X-Silo-Client",
	"X-Silo-Client-Version",
	"X-Silo-Client-Build",
	"X-Silo-Client-Channel",
}

func aliasOf(t *testing.T, canonical string) string {
	t.Helper()
	alias, ok := clientHeaderAliases[canonical]
	if !ok {
		t.Fatalf("no Bloem alias registered for %s", canonical)
	}
	return alias
}

// seenHeaders runs the middleware over req and reports the headers the next
// handler in the chain observes.
func seenHeaders(req *http.Request) http.Header {
	var seen http.Header
	NormalizeClientHeaders(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
	})).ServeHTTP(httptest.NewRecorder(), req)
	return seen
}

func newRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/api/v1/settings/values", nil)
}

// Every header in the family is covered in all four presence combinations:
// Silo only, Bloem only, both, neither.
func TestNormalizeClientHeadersPresenceMatrix(t *testing.T) {
	for _, canonical := range canonicalClientHeaders {
		alias := aliasOf(t, canonical)

		t.Run(canonical+"/silo only", func(t *testing.T) {
			req := newRequest()
			req.Header.Set(canonical, "silo-value")
			seen := seenHeaders(req)
			if got := seen.Get(canonical); got != "silo-value" {
				t.Fatalf("%s = %q, want %q", canonical, got, "silo-value")
			}
			if _, present := seen[textproto.CanonicalMIMEHeaderKey(alias)]; present {
				t.Fatalf("%s appeared from nowhere", alias)
			}
		})

		t.Run(canonical+"/bloem only", func(t *testing.T) {
			req := newRequest()
			req.Header.Set(alias, "bloem-value")
			seen := seenHeaders(req)
			if got := seen.Get(canonical); got != "bloem-value" {
				t.Fatalf("%s = %q, want the Bloem value %q", canonical, got, "bloem-value")
			}
			if _, present := seen[textproto.CanonicalMIMEHeaderKey(alias)]; present {
				t.Fatalf("%s survived normalisation; exactly one spelling must reach handlers", alias)
			}
		})

		t.Run(canonical+"/both", func(t *testing.T) {
			req := newRequest()
			req.Header.Set(canonical, "silo-value")
			req.Header.Set(alias, "bloem-value")
			seen := seenHeaders(req)
			// The canonical spelling wins: a request that already carries the
			// value the server has always honored keeps behaving identically.
			if got := seen.Get(canonical); got != "silo-value" {
				t.Fatalf("%s = %q, want the canonical value to win", canonical, got)
			}
			if _, present := seen[textproto.CanonicalMIMEHeaderKey(alias)]; present {
				t.Fatalf("%s survived normalisation", alias)
			}
		})

		t.Run(canonical+"/neither", func(t *testing.T) {
			seen := seenHeaders(newRequest())
			if _, present := seen[textproto.CanonicalMIMEHeaderKey(canonical)]; present {
				t.Fatalf("%s was invented for a request that sent no identity", canonical)
			}
			if _, present := seen[textproto.CanonicalMIMEHeaderKey(alias)]; present {
				t.Fatalf("%s was invented for a request that sent no identity", alias)
			}
		})
	}
}

// An empty value is not a present value, on either side.
func TestNormalizeClientHeadersTreatsEmptyAsAbsent(t *testing.T) {
	canonical := "X-Silo-Client-Family"
	alias := aliasOf(t, canonical)

	t.Run("empty canonical is filled from the alias", func(t *testing.T) {
		req := newRequest()
		req.Header.Set(canonical, "   ")
		req.Header.Set(alias, "tv")
		if got := seenHeaders(req).Get(canonical); got != "tv" {
			t.Fatalf("%s = %q, want the alias to fill an empty canonical", canonical, got)
		}
	})

	t.Run("empty alias fills nothing", func(t *testing.T) {
		req := newRequest()
		req.Header.Set(alias, "  ")
		seen := seenHeaders(req)
		if _, present := seen[textproto.CanonicalMIMEHeaderKey(canonical)]; present {
			t.Fatalf("%s was created from an empty alias: %q", canonical, seen.Get(canonical))
		}
	})

	t.Run("empty alias does not blank a real canonical", func(t *testing.T) {
		req := newRequest()
		req.Header.Set(canonical, "mobile")
		req.Header.Set(alias, "")
		if got := seenHeaders(req).Get(canonical); got != "mobile" {
			t.Fatalf("%s = %q, want %q", canonical, got, "mobile")
		}
	})

	t.Run("first non-empty alias value is used", func(t *testing.T) {
		req := newRequest()
		req.Header.Add(alias, "")
		req.Header.Add(alias, "desktop")
		if got := seenHeaders(req).Get(canonical); got != "desktop" {
			t.Fatalf("%s = %q, want %q", canonical, got, "desktop")
		}
	})
}

// An upstream-compatible client sends only X-Silo-*. Such a request must reach
// the handler byte for byte as it was sent — this pins that the middleware is a
// no-op over the whole header set, not merely over the names it knows.
func TestNormalizeClientHeadersLeavesUpstreamCompatRequestsUntouched(t *testing.T) {
	req := newRequest()
	req.Header.Set("Authorization", "Bearer upstream-token")
	req.Header.Set("X-Profile-Id", "reader")
	req.Header.Set("X-Silo-Device-Id", "living-room-tv")
	req.Header.Set("X-Silo-Device-Name", "Living Room")
	req.Header.Set("X-Silo-Device-Platform", "androidtv")
	req.Header.Set("X-Silo-Client-Family", "tv")
	req.Header.Set("X-Silo-Client", "Silo TV")
	req.Header.Set("X-Silo-Client-Version", "3.1.0")
	req.Header.Set("X-Silo-Client-Build", "4711")
	req.Header.Set("X-Silo-Client-Channel", "beta")
	req.Header.Set("X-Silo-Mutation-Id", "mutation-1")
	sent := req.Header.Clone()

	seen := seenHeaders(req)
	if !reflect.DeepEqual(map[string][]string(sent), map[string][]string(seen)) {
		t.Fatalf("upstream-compat request was altered:\n sent %v\n seen %v", sent, seen)
	}
}

// The guard in RequireAuth refuses a declared device id that conflicts with the
// session's binding, and the claim-derived value wins. Normalisation runs ahead
// of it, so the guard must apply to a device declared under either spelling —
// getting the order wrong would let a session act as a device it never
// authenticated. A later refactor that reorders the two fails here.
func TestDeclaredDeviceIDConflictIsRefusedUnderBothSpellings(t *testing.T) {
	claims := &auth.Claims{
		UserID: 7, ProfileID: "reader", SessionID: "session-1", DeviceID: "bound-device",
		TokenType: auth.TokenTypeAccess, AuthMethod: auth.AuthMethodDirectProfile,
	}

	var handlerSawDeviceID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerSawDeviceID = r.Header.Get("X-Silo-Device-Id")
		w.WriteHeader(http.StatusNoContent)
	})
	chain := func() http.Handler {
		am := NewAuthMiddleware(staticTokenValidator{claims: claims}, alwaysValidSession{}, nil, nil)
		am.SetDirectProfileRouteGuard(func(*http.Request) bool { return true })
		return NormalizeClientHeaders(am.RequireAuth(next))
	}

	tests := []struct {
		name       string
		header     string
		value      string
		wantStatus int
		wantDevice string
	}{
		{"silo spelling conflicts", "X-Silo-Device-Id", "somebody-elses-device", http.StatusForbidden, ""},
		{"bloem spelling conflicts", "X-Bloem-Device-Id", "somebody-elses-device", http.StatusForbidden, ""},
		{"silo spelling agrees", "X-Silo-Device-Id", "bound-device", http.StatusNoContent, "bound-device"},
		{"bloem spelling agrees", "X-Bloem-Device-Id", "bound-device", http.StatusNoContent, "bound-device"},
		{"no device declared", "", "", http.StatusNoContent, "bound-device"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handlerSawDeviceID = ""
			req := newRequest()
			req.Header.Set("Authorization", "Bearer direct-token")
			if test.header != "" {
				req.Header.Set(test.header, test.value)
			}
			rec := httptest.NewRecorder()
			chain().ServeHTTP(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, test.wantStatus)
			}
			if handlerSawDeviceID != test.wantDevice {
				t.Fatalf("handler saw device %q, want %q", handlerSawDeviceID, test.wantDevice)
			}
		})
	}

	// Both spellings present and the Bloem one conflicting: the canonical value
	// decides, and the conflicting alias must not survive to be read later.
	t.Run("canonical agrees while alias conflicts", func(t *testing.T) {
		handlerSawDeviceID = ""
		req := newRequest()
		req.Header.Set("Authorization", "Bearer direct-token")
		req.Header.Set("X-Silo-Device-Id", "bound-device")
		req.Header.Set("X-Bloem-Device-Id", "somebody-elses-device")
		rec := httptest.NewRecorder()
		chain().ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
		}
		if handlerSawDeviceID != "bound-device" {
			t.Fatalf("handler saw device %q, want %q", handlerSawDeviceID, "bound-device")
		}
	})
}
