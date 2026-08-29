package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
)

// extractClaims previously only accepted a JWT, so a long-lived "sa_" API
// key — which authenticates successfully against every route guarded by
// AuthMiddleware.RequireAuth — was silently rejected here with "invalid or
// missing authentication token", even though the key was perfectly valid.
// These pin the fix: same key format, same result, on this path too.

type stubAPIKeyValidator struct {
	key           *models.APIKey
	err           error
	lastUsedCalls []int64
}

func (s *stubAPIKeyValidator) GetByKey(_ context.Context, _ string) (*models.APIKey, error) {
	return s.key, s.err
}

func (s *stubAPIKeyValidator) UpdateLastUsed(_ context.Context, id int64) error {
	s.lastUsedCalls = append(s.lastUsedCalls, id)
	return nil
}

type stubAPIKeyUserLoader struct {
	user *models.User
	err  error
}

func (s *stubAPIKeyUserLoader) GetByID(_ context.Context, _ int) (*models.User, error) {
	return s.user, s.err
}

func newAPIKeyRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestExtractClaimsAcceptsAValidAPIKey(t *testing.T) {
	t.Parallel()
	validator := &stubAPIKeyValidator{key: &models.APIKey{ID: 22, UserID: 131, RateTier: "standard"}}
	incarnation := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	loader := &stubAPIKeyUserLoader{user: &models.User{ID: 131, AccountIncarnationID: incarnation, Role: "user", Enabled: true}}
	handler := &AuthHandler{apiKeyValidator: validator, apiKeyUserLoader: loader}

	claims, err := handler.extractClaims(newAPIKeyRequest("sa_demo-token"))
	if err != nil {
		t.Fatalf("extractClaims: %v", err)
	}
	if claims.UserID != 131 || claims.Role != "user" {
		t.Fatalf("claims = %#v, want UserID=131 Role=user", claims)
	}
	if claims.TokenType != auth.TokenTypeAPIKey {
		t.Fatalf("TokenType = %q, want %q", claims.TokenType, auth.TokenTypeAPIKey)
	}
	if claims.APIKeyID != 22 {
		t.Fatalf("APIKeyID = %d, want 22", claims.APIKeyID)
	}
	if claims.RateTier != "standard" {
		t.Fatalf("RateTier = %q, want %q", claims.RateTier, "standard")
	}
	if claims.AccountIncarnationID != incarnation.String() {
		t.Fatalf("AccountIncarnationID = %q", claims.AccountIncarnationID)
	}
}

func TestExtractClaimsRejectsUnknownAPIKey(t *testing.T) {
	t.Parallel()
	validator := &stubAPIKeyValidator{err: errors.New("not found")}
	loader := &stubAPIKeyUserLoader{}
	handler := &AuthHandler{apiKeyValidator: validator, apiKeyUserLoader: loader}

	if _, err := handler.extractClaims(newAPIKeyRequest("sa_wrong")); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("err = %v, want auth.ErrInvalidToken", err)
	}
}

func TestExtractClaimsRejectsDisabledUsersAPIKey(t *testing.T) {
	t.Parallel()
	validator := &stubAPIKeyValidator{key: &models.APIKey{ID: 22, UserID: 131}}
	loader := &stubAPIKeyUserLoader{user: &models.User{ID: 131, Enabled: false}}
	handler := &AuthHandler{apiKeyValidator: validator, apiKeyUserLoader: loader}

	if _, err := handler.extractClaims(newAPIKeyRequest("sa_demo-token")); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("err = %v, want auth.ErrInvalidToken", err)
	}
}

// Without SetAPIKeyAuth (the zero value, matching every AuthHandler built
// before this fix), an "sa_" token must fail closed rather than panic on a
// nil validator.
func TestExtractClaimsRejectsAPIKeyWhenNotConfigured(t *testing.T) {
	t.Parallel()
	handler := &AuthHandler{}

	if _, err := handler.extractClaims(newAPIKeyRequest("sa_demo-token")); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("err = %v, want auth.ErrInvalidToken", err)
	}
}

// A missing/malformed header is unrelated to the API-key path and must keep
// behaving exactly as before.
func TestExtractClaimsRejectsMissingHeader(t *testing.T) {
	t.Parallel()
	handler := &AuthHandler{}

	if _, err := handler.extractClaims(newAPIKeyRequest("")); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("err = %v, want auth.ErrInvalidToken", err)
	}
}
