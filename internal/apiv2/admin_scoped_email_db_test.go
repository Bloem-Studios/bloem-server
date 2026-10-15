package apiv2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/mail"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/passwordreset"
)

type recoveryMailCapture struct{ messages chan mail.Message }

func (*recoveryMailCapture) Enabled(context.Context) bool { return true }
func (m *recoveryMailCapture) Send(_ context.Context, msg mail.Message) error {
	m.messages <- msg
	return nil
}

type recoverySettings struct{}

func (recoverySettings) Get(_ context.Context, key string) (string, error) {
	if key == passwordreset.SettingSelfService {
		return "true", nil
	}
	return "", nil
}

// Exercise account persistence, scoped-key authentication and the public
// recovery flow together. Recovery must remain usable at the original address
// after a scoped key tries to redirect it, including on its own admin account.
func scopedRecoveryDB(t *testing.T) *pgxpool.Pool {
	return bloemScopedRecoveryDB(t)
}

func TestScopedAdminKeyCannotRedirectRecoveryDB(t *testing.T) {
	// A private schema keeps the Owner cases independent of existing accounts
	// and other packages running against the same database.
	for _, version := range []string{"v1", "v2"} {
		for _, owner := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/owner=%t", version, owner), func(t *testing.T) {
				pool := scopedRecoveryDB(t)
				ctx := t.Context()
				users := auth.NewUserRepository(pool)
				name := "scoped-recovery-" + uuid.NewString()
				originalEmail := name + "@example.test"
				admin, err := users.Create(ctx, models.CreateUserInput{Username: name, Email: originalEmail, Password: "original-password", Role: models.RoleAdmin})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = users.Delete(context.WithoutCancel(ctx), admin.ID) })
				if owner {
					if _, err := pool.Exec(ctx, `UPDATE users SET is_owner = true WHERE id = $1`, admin.ID); err != nil {
						t.Fatal(err)
					}
				}
				keys := auth.NewAPIKeyRepository(pool)
				key, err := keys.Create(ctx, admin.ID, "account integration", []string{auth.ScopeAdminUsers})
				if err != nil {
					t.Fatal(err)
				}
				sessions := auth.NewSessionRepository(pool)
				jwt := auth.NewJWTService("synthetic-scoped-recovery-jwt-secret", time.Minute, time.Hour)
				login := auth.NewService(auth.NewLocalProvider(users, sessions), jwt, sessions, users, nil, nil, nil)
				capture := &recoveryMailCapture{messages: make(chan mail.Message, 1)}
				resets := passwordreset.NewService(passwordreset.NewRepository(pool), users, login, capture, recoverySettings{}, nil, "https://silo.example.test")
				accounts := handlers.NewAdminHandler(users, pool, nil)
				middleware := apimw.NewAuthMiddleware(jwt, sessions, keys, users)
				deps := parityDeps(false)
				deps.Auth, deps.AdminAccounts = middleware, accounts
				deps.PasswordResets = handlers.NewPasswordResetHandler(resets, users)
				v2 := NewHandler(deps)
				router := chi.NewRouter()
				router.With(middleware.RequireAuth, apimw.RequireAdmin).Put("/api/v1/admin/users/{id}", accounts.HandleUpdateUser)
				router.Handle(Prefix+"/*", v2)
				path := fmt.Sprintf("/api/%s/admin/users/%d", version, admin.ID)
				headers := with(bearer(key.Key), "If-Match", "*")
				attackerEmail := "attacker-" + name + "@example.test"
				update := do(t, router, http.MethodPut, path, fmt.Sprintf(`{"email":%q}`, attackerEmail), headers)
				if update.Code != http.StatusForbidden {
					t.Errorf("email update: status %d, want 403: %s", update.Code, update.Body)
				} else if version == "v1" {
					var body struct {
						Error string `json:"error"`
					}
					if err := json.Unmarshal(update.Body.Bytes(), &body); err != nil || body.Error != "insufficient_scope" {
						t.Fatalf("email denial: %s (%v)", update.Body, err)
					}
				} else {
					requireProblem(t, update, TypePermissionDenied)
				}
				stored, err := users.GetByID(ctx, admin.ID)
				if err != nil {
					t.Fatal(err)
				}
				if stored.Email != originalEmail {
					t.Errorf("stored recovery email = %q, want %q", stored.Email, originalEmail)
				}
				// A metadata-only update remains in scope for the same key. The
				// profile limit is access policy, so only the Owner's key may
				// change it on its own admin account.
				metadata := do(t, router, http.MethodPut, path, `{"max_profiles":6}`, headers)
				wantStatus := http.StatusNoContent
				if version == "v1" {
					wantStatus = http.StatusOK
				}
				if owner {
					if metadata.Code != wantStatus {
						t.Fatalf("metadata update: %d %s", metadata.Code, metadata.Body)
					}
					stored, err = users.GetByID(ctx, admin.ID)
					if err != nil || stored.MaxProfiles == nil || *stored.MaxProfiles != 6 {
						t.Fatalf("metadata update was not persisted: %+v, %v", stored, err)
					}
				} else {
					if metadata.Code != http.StatusForbidden {
						t.Fatalf("metadata update: %d %s, want 403", metadata.Code, metadata.Body)
					}
					stored, err = users.GetByID(ctx, admin.ID)
					if err != nil || stored.MaxProfiles != nil {
						t.Fatalf("refused metadata update was persisted: %+v, %v", stored, err)
					}
				}
				requested := do(t, router, http.MethodPost, Prefix+"/password-resets", fmt.Sprintf(`{"login":%q}`, name), nil)
				if requested.Code != http.StatusAccepted {
					t.Fatalf("recovery request: %d %s", requested.Code, requested.Body)
				}
				var message mail.Message
				select {
				case message = <-capture.messages:
				case <-time.After(5 * time.Second):
					t.Fatal("recovery did not deliver a message")
				}
				if len(message.To) != 1 || message.To[0] != originalEmail {
					t.Errorf("reset delivered to %v, want original address %q", message.To, originalEmail)
				}
				token := regexp.MustCompile(`/reset-password/([A-Za-z0-9_-]+)`).FindStringSubmatch(message.TextBody)
				if len(token) != 2 {
					t.Fatal("recovery message has no reset link")
				}
				// Complete the captured link to prove recovery can issue a full
				// admin session; its delivery address is the security boundary.
				completed := do(t, router, http.MethodPost, Prefix+"/password-resets/"+token[1]+"/complete", `{"password":"replacement-password"}`, nil)
				var body PasswordResetCompletion
				if err := json.Unmarshal(completed.Body.Bytes(), &body); err != nil || completed.Code != http.StatusOK || body.LoginStatus != "signed_in" || body.Tokens == nil {
					t.Fatalf("recovery completion: %d %s (%v)", completed.Code, completed.Body, err)
				}
				claims, err := jwt.ValidateToken(body.Tokens.AccessToken)
				if err != nil || claims.UserID != admin.ID || claims.Role != models.RoleAdmin || !claims.IsOwnLoginSession() || claims.PasswordChangeRequired {
					t.Fatalf("recovery did not issue a full admin login: %+v, %v", claims, err)
				}
				stored, err = users.GetByID(ctx, admin.ID)
				if err != nil || !auth.CheckPassword(stored, "replacement-password") {
					t.Fatal("recovery did not replace the password", err)
				}
				// The same account and email write succeed through its full login,
				// so the earlier refusal cannot come from an Owner restriction.
				fullLogin := with(bearer(body.Tokens.AccessToken), "If-Match", "*")
				allowed := do(t, router, http.MethodPut, path, fmt.Sprintf(`{"email":%q}`, attackerEmail), fullLogin)
				if allowed.Code != wantStatus {
					t.Fatalf("full login email update: %d %s", allowed.Code, allowed.Body)
				}
				stored, err = users.GetByID(ctx, admin.ID)
				if err != nil || stored.Email != attackerEmail {
					t.Fatalf("full login email update was not persisted: %+v, %v", stored, err)
				}
			})
		}
	}
}
