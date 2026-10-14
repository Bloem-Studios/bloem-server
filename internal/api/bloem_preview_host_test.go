package api

import (
	"context"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/preview"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/activitylog"
	"github.com/Silo-Server/silo-server/internal/auth"
)

type hostSettings struct{ s auth.PreviewGuestSettings }

func (h hostSettings) PreviewGuestSettings(context.Context) (auth.PreviewGuestSettings, error) {
	return h.s, nil
}

type hostTokens map[string]*auth.Claims

func (h hostTokens) ValidateToken(tok string) (*auth.Claims, error) {
	if c, ok := h[tok]; ok {
		return c, nil
	}
	return nil, auth.ErrInvalidToken
}

func TestPreviewHostGuard(t *testing.T) {
	settings := hostSettings{auth.PreviewGuestSettings{Enabled: true, Host: "browse.example.test", AccountID: 7}}
	tokens := hostTokens{
		"guest":  {UserID: 7, AuthMethod: auth.AuthMethodPreviewGuest, TokenType: auth.TokenTypeAccess},
		"member": {UserID: 8, TokenType: auth.TokenTypeAccess},
	}
	var sawHost string
	guard := previewHostGuard(settings, tokens)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHost = auth.RequestHostFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	serve := func(host, method, path, bearer string) int {
		r := httptest.NewRequest(method, path, nil)
		r.Host = host
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		r.Header.Set("X-Forwarded-Host", "lib.example.test")
		w := httptest.NewRecorder()
		guard.ServeHTTP(w, r)
		return w.Code
	}
	for _, tc := range []struct {
		host, method, path, bearer string
		want                       int
	}{
		{"browse.example.test", "POST", "/api/v2/auth/login", "", http.StatusForbidden},
		{"browse.example.test", "POST", "/api/v1/auth/login", "", http.StatusForbidden},
		{"browse.example.test", "GET", "/api/v2/auth/oauth/x/start", "", http.StatusForbidden},
		{"browse.example.test", "POST", "/api/v2/auth/device/start", "", http.StatusForbidden},
		{"browse.example.test", "POST", "/api/v2/password-resets", "", http.StatusForbidden},
		{"browse.example.test", "GET", "/api/v1/plugins/1/", "", http.StatusForbidden},
		{"browse.example.test", "GET", "/api/v2/plugin-content/plugins/1/", "", http.StatusForbidden},
		{"browse.example.test", "POST", "/api/v2/auth/refresh", "", http.StatusNoContent},
		{"browse.example.test", "POST", "/api/v1/auth/refresh", "", http.StatusNoContent},
		{"browse.example.test", "POST", "/api/v2/auth/logout", "guest", http.StatusNoContent},
		{"browse.example.test", "POST", "/api/bloem/v1/preview/sessions", "", http.StatusNoContent},
		{"browse.example.test", "GET", "/api/v2/catalog/items/x", "guest", http.StatusNoContent},
		{"browse.example.test", "GET", "/api/v2/catalog/items/x", "member", http.StatusUnauthorized},
		{"browse.example.test", "GET", "/api/v2/catalog/items/x", "sa_key", http.StatusUnauthorized},
		{"browse.example.test", "GET", "/", "", http.StatusNoContent},
		// Elsewhere the guard does nothing; RequireAuth refuses guests there.
		{"lib.example.test", "POST", "/api/v2/auth/login", "", http.StatusNoContent},
		{"lib.example.test", "GET", "/api/v2/catalog/items/x", "member", http.StatusNoContent},
	} {
		if got := serve(tc.host, tc.method, tc.path, tc.bearer); got != tc.want {
			t.Errorf("%s %s %s (%s) = %d, want %d", tc.host, tc.method, tc.path, tc.bearer, got, tc.want)
		}
	}
	serve("Browse.Example.Test:443", "GET", "/", "")
	if sawHost != "browse.example.test" {
		t.Fatalf("request host in context = %q, want the Host header, never X-Forwarded-Host", sawHost)
	}
}

type recordedActivity struct{ entries []activitylog.LogEntry }

func (r *recordedActivity) Write(e activitylog.LogEntry) { r.entries = append(r.entries, e) }

func TestGuestActivityIsNotRecorded(t *testing.T) {
	rec := &recordedActivity{}
	w := guestFreeActivity(rec, hostSettings{auth.PreviewGuestSettings{Enabled: true, Host: "browse.example.test", AccountID: 7}})
	guest, member := 7, 8
	w.Write(activitylog.LogEntry{UserID: &guest, ClientIP: "198.51.100.1", UserAgent: "ua"})
	w.Write(activitylog.LogEntry{Path: "/api/bloem/v1/preview/sessions", ClientIP: "198.51.100.1"})
	w.Write(activitylog.LogEntry{UserID: &member, ClientIP: "198.51.100.2"})
	if len(rec.entries) != 1 || *rec.entries[0].UserID != member {
		t.Fatalf("recorded %+v, want only the member", rec.entries)
	}
}

func TestPreviewRequestsHideAddressesAndKeepAdmissionBuckets(t *testing.T) {
	settings := hostSettings{auth.PreviewGuestSettings{Enabled: true, Host: "browse.example.test", AccountID: 7}}
	tokens := hostTokens{"guest": {AuthMethod: auth.AuthMethodPreviewGuest}, "member": {AuthMethod: "account"}}
	for _, tc := range []struct {
		host, path, bearer, body string
		guest                    bool
	}{
		{"browse.example.test", "/api/v2/catalog/items/x", "", "", true},
		{"lib.example.test", "/api/v2/catalog/items/x", "guest", "", true},
		{"lib.example.test", "/api/v2/auth/refresh", "", `{"refresh_token":"guest"}`, true},
		{"lib.example.test", "/api/v2/catalog/items/x", "member", "", false},
	} {
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		r.Host = tc.host
		r = r.WithContext(clientip.SetContext(r.Context(), "198.51.100.12"))
		r.Header.Set("User-Agent", "private-browser")
		if tc.bearer != "" {
			r.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if apimw.PreviewClientIP(r.Context()) != "198.51.100.12" {
				t.Error("admission lost client bucket")
			}
			if tc.guest && (clientip.FromContext(r.Context()) != "" || r.UserAgent() != "" || !apimw.IsPreviewRequest(r.Context())) {
				t.Error("guest details reach observers")
			}
			if !tc.guest && (clientip.FromContext(r.Context()) != "198.51.100.12" || r.UserAgent() != "private-browser") {
				t.Error("member details were removed")
			}
			got, _ := io.ReadAll(r.Body)
			if string(got) != tc.body {
				t.Error("refresh body changed")
			}
		})
		previewHostGuard(settings, tokens)(next).ServeHTTP(httptest.NewRecorder(), r)
		if !called {
			t.Errorf("handler not called for %s", tc.path)
		}
	}
}

func (r *recordedActivity) Close() error { return nil }

func TestGuestActivityUsesOnlyTheCachedAccount(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	user, err := auth.NewUserRepository(pool).Create(t.Context(), models.CreateUserInput{Username: "logging-preview", Email: "logging-preview@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	store := preview.NewStore(pool)
	if err := store.Put(t.Context(), preview.Settings{AccountID: user.ID, SessionTTL: 2 * time.Hour, IdleTTL: 20 * time.Minute, MaxConcurrent: 200}); err != nil {
		t.Fatal(err)
	}
	reader := preview.NewReader(store, 0)
	if _, err := reader.Settings(t.Context()); err != nil {
		t.Fatal(err)
	}
	rec := &recordedActivity{}
	writer := guestFreeActivity(rec, reader)
	pool.Close()
	writer.Write(activitylog.LogEntry{UserID: &user.ID, ClientIP: "198.51.100.12"})
	if len(rec.entries) != 0 {
		t.Fatal("a database outage caused guest activity to be persisted")
	}
}

func (h hostSettings) CachedAccountID() int { return h.s.AccountID }

func TestPreviewHostCapsPublicGuestTraffic(t *testing.T) {
	guard := previewHostGuard(hostSettings{auth.PreviewGuestSettings{Host: "browse.example.test"}}, hostTokens{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	admitted := 0
	for range 180 {
		r := httptest.NewRequest("GET", "/api/bloem/v1/preview/status", nil)
		r.Host = "browse.example.test"
		w := httptest.NewRecorder()
		guard.ServeHTTP(w, r)
		if w.Code == http.StatusNoContent {
			admitted++
		}
	}
	if admitted > 125 {
		t.Fatalf("public guest requests admitted=%d; aggregate cap was bypassed", admitted)
	}
	r := httptest.NewRequest("GET", "/normal", nil)
	r.Host = "lib.example.test"
	w := httptest.NewRecorder()
	guard.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatal("guest cap blocked a member host")
	}
}
