package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
)

// TestDisabledMainStopsLoginRequests: a household login signed in with a
// valid access token is refused on its next request once its main account is
// disabled, and admitted again once the main account comes back and the
// login signs in afresh.
func TestDisabledMainStopsLoginRequests(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	jwt := auth.NewJWTService("household-logins-secret-0123456789abcdef", time.Hour, time.Hour)
	sessions := auth.NewSessionRepository(pool)
	users := auth.NewUserRepository(pool)
	chain := NewAuthMiddleware(jwt, sessions, auth.NewAPIKeyRepository(pool), users).RequireAuth(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	account := func(main *int) int {
		t.Helper()
		var id int
		name := fmt.Sprintf("household-%d", time.Now().UnixNano())
		if err := pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash, role, household_main_user_id) VALUES ($1, $2, '', 'user', $3) RETURNING id`,
			name, name+"@example.test", main).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM auth_sessions WHERE user_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		})
		return id
	}
	signIn := func(userID int) string {
		t.Helper()
		session := models.AuthSession{ID: fmt.Sprintf("household-session-%d-%d", userID, time.Now().UnixNano()), UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}
		if err := sessions.Create(ctx, session); err != nil {
			t.Fatal(err)
		}
		token, err := jwt.GenerateAccessToken(userID, models.RoleUser, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	serve := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/home", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		return rec.Code
	}

	main := account(nil)
	login := account(&main)
	token := signIn(login)
	if code := serve(token); code != http.StatusNoContent {
		t.Fatalf("login before its main account is disabled: %d", code)
	}
	if err := users.Update(ctx, main, models.UpdateUserInput{Enabled: new(false)}); err != nil {
		t.Fatal(err)
	}
	if code := serve(token); code != http.StatusUnauthorized {
		t.Fatalf("login after its main account was disabled: %d; want 401", code)
	}
	if err := users.Update(ctx, main, models.UpdateUserInput{Enabled: new(true)}); err != nil {
		t.Fatal(err)
	}
	if code := serve(signIn(login)); code != http.StatusNoContent {
		t.Fatalf("login after its main account came back: %d", code)
	}
}
