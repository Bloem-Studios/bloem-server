package middleware

import (
	"context"
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

func (p previewSessions) ActiveSessionRole(_ context.Context, id string) (string, bool, error) {
	return "user", true, nil
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
