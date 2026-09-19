package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestBloemLogoutRefusesSessionlessClaims(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims *auth.Claims
	}{
		{"api_key", &auth.Claims{UserID: 1, TokenType: auth.TokenTypeAPIKey}},
		{"api_key_with_session", &auth.Claims{UserID: 1, TokenType: auth.TokenTypeAPIKey, SessionID: uuid.NewString()}},
		{"sessionless_access", &auth.Claims{UserID: 1, TokenType: auth.TokenTypeAccess}},
		{"sessionless_direct_profile", &auth.Claims{UserID: 1, TokenType: auth.TokenTypeAccess, AuthMethod: auth.AuthMethodDirectProfile, ProfileID: "profile"}},
		{"missing_claims", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No service is installed: credential refusal must precede revocation.
			err := (&AuthHandler{}).Logout(t.Context(), tc.claims)
			var decision *APIError
			if !errors.As(err, &decision) || decision.Status != http.StatusUnauthorized || decision.Code != "unauthorized" {
				t.Fatalf("logout decision = %v, want 401 unauthorized", err)
			}
		})
	}
}

func TestBloemLogoutAPIKeyUnauthorizedContract(t *testing.T) {
	handler := &AuthHandler{
		apiKeyValidator:  &stubAPIKeyValidator{key: &models.APIKey{ID: 22, UserID: 131}},
		apiKeyUserLoader: &stubAPIKeyUserLoader{user: &models.User{ID: 131, AccountIncarnationID: uuid.New(), Role: models.RoleUser, Enabled: true}},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer sa_logout-test-key")
	response := httptest.NewRecorder()
	handler.HandleLogout(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("logout status = %d, want 401", response.Code)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal("logout response is not JSON")
	}
	if body.Error != "unauthorized" {
		t.Fatalf("logout error code = %q, want unauthorized", body.Error)
	}
}

func TestBloemLogoutRevokesOnlyCallerSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	pool := newInvitationLifecycleDatabase(t, ctx)
	users := auth.NewUserRepository(pool)
	accounts := auth.NewAccountProvisioner(users, pgstore.NewPostgresProvider(pool))
	accounts.SetMembershipProvisioner(invitationLifecycleMemberships{store: tenancy.NewStore(pool)})
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	created, err := accounts.CreateAccountInTransaction(ctx, tx, auth.CreateAccountInput{
		User:           models.CreateUserInput{Username: "logout", Email: "logout@example.test", Password: "logout-test-password", Role: models.RoleUser},
		DefaultProfile: auth.DefaultProfileOptions{Enabled: true, Name: "Logout"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT credential_revision FROM user_profiles WHERE user_id=$1 AND id=$2`, created.User.ID, created.ProfileID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionRepository(pool)
	const secret = "bloem-logout-test-secret"
	tokens := auth.NewJWTService(secret, time.Hour, 24*time.Hour)
	handler := NewAuthHandler(auth.NewService(nil, tokens, sessions, users, nil, nil, nil), tokens, nil)
	control := models.AuthSession{ID: uuid.NewString(), UserID: created.User.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := sessions.Create(ctx, control); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"account", auth.AuthMethodDirectProfile} {
		t.Run(method, func(t *testing.T) {
			session := models.AuthSession{ID: uuid.NewString(), UserID: created.User.ID, ExpiresAt: time.Now().Add(time.Hour), AuthMethod: method}
			claims := auth.Claims{
				UserID: created.User.ID, AccountIncarnationID: created.User.AccountIncarnationID.String(), Role: created.User.Role,
				SessionID: session.ID, TokenType: auth.TokenTypeAccess, AuthMethod: method,
				RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(session.ExpiresAt)},
			}
			if method == auth.AuthMethodDirectProfile {
				session.ProfileID, session.ProfileCredentialRevision, session.DeviceID = &created.ProfileID, &revision, "logout-device"
				claims.ProfileID, claims.CredentialRevision, claims.DeviceID = created.ProfileID, revision, session.DeviceID
				claims.OrganizationID, claims.MembershipID = created.OrganizationID.String(), created.MembershipID.String()
			}
			if err := sessions.Create(ctx, session); err != nil {
				t.Fatal(err)
			}
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
			if err != nil {
				t.Fatal("sign logout fixture token")
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			handler.HandleLogout(response, req)
			if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
				t.Fatalf("logout = %d, body bytes = %d; want empty 204", response.Code, response.Body.Len())
			}
			for id, wantRevoked := range map[string]bool{session.ID: true, control.ID: false} {
				stored, err := sessions.GetByID(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if (stored.RevokedAt != nil) != wantRevoked {
					t.Fatalf("session revocation = %v, want %v", stored.RevokedAt != nil, wantRevoked)
				}
			}
		})
	}
}
