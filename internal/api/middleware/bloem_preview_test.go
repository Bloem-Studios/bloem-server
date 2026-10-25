package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
)

type previewTokens map[string]*auth.Claims

func (p previewTokens) ValidateToken(tok string) (*auth.Claims, error) {
	if c, ok := p[tok]; ok {
		return c, nil
	}
	return nil, auth.ErrInvalidToken
}

type previewSessions struct {
	preview  map[string]bool
	accounts map[int]bool
}

func (p previewSessions) ActiveSession(_ context.Context, id string) (auth.SessionAuthority, bool, error) {
	return auth.SessionAuthority{Role: "user"}, true, nil
}

func (p previewSessions) ActiveSessionAuthority(_ context.Context, id string) (string, bool, bool, error) {
	return "user", p.preview[id], true, nil
}

func (p previewSessions) AccountPreviewOnly(_ context.Context, accountID int) (bool, error) {
	return p.accounts[accountID], nil
}

func previewMiddleware(guard PreviewRouteGuard) (*AuthMiddleware, previewSessions) {
	sessions := previewSessions{preview: map[string]bool{"s-preview": true}, accounts: map[int]bool{9: true}}
	tokens := previewTokens{
		"preview":  {UserID: 9, Role: "user", TokenType: auth.TokenTypeAccess, SessionID: "s-preview"},
		"standard": {UserID: 8, Role: "user", TokenType: auth.TokenTypeAccess, SessionID: "s-standard"},
	}
	am := NewAuthMiddleware(tokens, sessions, nil, nil)
	am.SetPreviewPrincipals(sessions)
	if guard != nil {
		am.SetPreviewRouteGuard(guard)
	}
	return am, sessions
}

func serveWithBearer(am *AuthMiddleware, token, path string) (*httptest.ResponseRecorder, bool) {
	var marked bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		marked = IsPreviewPrincipal(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	am.RequireAuth(next).ServeHTTP(w, r)
	return w, marked
}

func TestRequireAuthMarksPreviewPrincipalsAndAppliesTheGuard(t *testing.T) {
	am, _ := previewMiddleware(func(r *http.Request) bool { return r.URL.Path == "/allowed" })
	if w, marked := serveWithBearer(am, "preview", "/allowed"); w.Code != http.StatusNoContent || !marked {
		t.Fatalf("allowed route: %d marked=%v", w.Code, marked)
	}
	w, _ := serveWithBearer(am, "preview", "/denied")
	if w.Code != http.StatusForbidden || w.Header().Get("X-Bloem-Denial") != CodePreviewOnly {
		t.Fatalf("denied route: %d %q %s", w.Code, w.Header().Get("X-Bloem-Denial"), w.Body)
	}
	if w, marked := serveWithBearer(am, "standard", "/denied"); w.Code != http.StatusNoContent || marked {
		t.Fatalf("standard account: %d marked=%v", w.Code, marked)
	}
}

func TestRequireAuthFailsClosedWithoutAPreviewGuard(t *testing.T) {
	am, _ := previewMiddleware(nil)
	if w, _ := serveWithBearer(am, "preview", "/anything"); w.Code != http.StatusForbidden {
		t.Fatalf("no guard installed: %d", w.Code)
	}
}

func TestRequireAuthRefusesPreviewAPIKeys(t *testing.T) {
	keys := previewAPIKeys{"sa_preview": {ID: 1, UserID: 9, Key: "sa_preview"}}
	users := previewUsers{9: {ID: 9, Role: "user", Enabled: true}}
	am := NewAuthMiddleware(nil, nil, keys, users)
	am.SetPreviewPrincipals(previewSessions{accounts: map[int]bool{9: true}})
	am.SetPreviewRouteGuard(func(*http.Request) bool { return true })
	if w, _ := serveWithBearer(am, "sa_preview", "/allowed"); w.Code != http.StatusForbidden || w.Header().Get("X-Bloem-Denial") != CodePreviewOnly {
		t.Fatalf("preview API key: %d %s", w.Code, w.Body)
	}
}

type previewAPIKeys map[string]*models.APIKey

func (p previewAPIKeys) GetByKey(_ context.Context, key string) (*models.APIKey, error) {
	if k, ok := p[key]; ok {
		return k, nil
	}
	return nil, auth.ErrAPIKeyNotFound
}

func (p previewAPIKeys) UpdateLastUsed(context.Context, int64) error { return nil }

type previewUsers map[int]*models.User

func (p previewUsers) GetByID(_ context.Context, id int) (*models.User, error) {
	if u, ok := p[id]; ok {
		return u, nil
	}
	return nil, auth.ErrNotFound
}

type guestSettings struct{ s auth.PreviewGuestSettings }

func (g guestSettings) PreviewGuestSettings(context.Context) (auth.PreviewGuestSettings, error) {
	return g.s, nil
}

func guestMiddleware(settings auth.PreviewGuestSettings, preview bool) *AuthMiddleware {
	sessions := previewSessions{preview: map[string]bool{"s-guest": preview}}
	tokens := previewTokens{"guest": {UserID: 7, Role: "user", TokenType: auth.TokenTypeAccess, SessionID: "s-guest", AuthMethod: auth.AuthMethodPreviewGuest}}
	am := NewAuthMiddleware(tokens, sessions, nil, nil)
	am.SetPreviewPrincipals(sessions)
	am.SetPreviewRouteGuard(func(*http.Request) bool { return true })
	am.SetPreviewGuests(guestSettings{settings}, NewPreviewGuestLimiter())
	return am
}

func serveGuest(am *AuthMiddleware, host string, header http.Header) (*httptest.ResponseRecorder, bool, bool) {
	var preview, guest bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		preview, guest = IsPreviewPrincipal(r.Context()), IsPreviewGuest(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Host = host
	for k, v := range header {
		r.Header[k] = v
	}
	r.Header.Set("Authorization", "Bearer guest")
	w := httptest.NewRecorder()
	am.RequireAuth(next).ServeHTTP(w, r)
	return w, preview, guest
}

func TestRequireAuthBindsGuestsToThePreviewHost(t *testing.T) {
	on := auth.PreviewGuestSettings{Enabled: true, Host: "browse.example.test", AccountID: 7}
	am := guestMiddleware(on, true)
	if w, preview, guest := serveGuest(am, "browse.example.test", nil); w.Code != http.StatusNoContent || !preview || !guest {
		t.Fatalf("preview host: %d preview=%v guest=%v", w.Code, preview, guest)
	}
	if w, _, _ := serveGuest(am, "Browse.Example.Test:443", nil); w.Code != http.StatusNoContent {
		t.Fatalf("preview host with port and case: %d", w.Code)
	}
	if w, _, _ := serveGuest(am, "lib.example.test", http.Header{"X-Forwarded-Host": {"browse.example.test"}}); w.Code != http.StatusUnauthorized {
		t.Fatalf("normal host claiming the preview host by X-Forwarded-Host: %d", w.Code)
	}
	off := on
	off.Enabled = false
	if w, _, _ := serveGuest(guestMiddleware(off, true), "browse.example.test", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("kill switch: %d", w.Code)
	}
	other := on
	other.AccountID = 8
	if w, _, _ := serveGuest(guestMiddleware(other, true), "browse.example.test", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("guest of another account: %d", w.Code)
	}
}

func TestGuestIsPreviewOnlyWhateverItsGroup(t *testing.T) {
	// The shared account's group no longer says preview (an admin swapped it):
	// a guest stays a preview principal.
	am := guestMiddleware(auth.PreviewGuestSettings{Enabled: true, Host: "browse.example.test", AccountID: 7}, false)
	if w, preview, _ := serveGuest(am, "browse.example.test", nil); w.Code != http.StatusNoContent || !preview {
		t.Fatalf("%d preview=%v", w.Code, preview)
	}
}

func TestPreviewGuestLimiterCapsOneGuestAndAllGuests(t *testing.T) {
	l := NewPreviewGuestLimiter()
	allowed := 0
	for range 100 {
		if l.Allow("one") {
			allowed++
		}
	}
	if allowed < 20 || allowed > 40 {
		t.Fatalf("one guest got %d of 100 immediate requests, want about its burst of 30", allowed)
	}
	total := 0
	for i := range 400 {
		if l.Allow(fmt.Sprintf("guest-%d", i)) {
			total++
		}
	}
	if total > 140 {
		t.Fatalf("all guests together got %d immediate requests, want about the burst of 120", total)
	}
}
