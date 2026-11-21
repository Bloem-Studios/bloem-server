package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// previewLoginBodyLimit is the most of a login body the guard reads: Silo's
// own request body limit for /api/v2. A larger body goes on unread, and Silo
// answers it with 413.
const previewLoginBodyLimit = 1 << 20

// Reject shared-account password sign-in before the ordinary handler can
// authenticate it and turn the database credential guard into a 500.
func previewAccountLoginGuard(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return previewAccountLoginGuardWith(func(ctx context.Context, username string) (bool, error) {
		var shared bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_preview_settings s JOIN user_login_identifiers i ON i.user_id=s.account_id WHERE i.identifier=$1)`, username).Scan(&shared)
		return shared, err
	})
}

// previewAccountLoginGuardWith is the guard with its shared-account lookup.
// Its refusals are Silo refusals (previewGuardRefusal); every other request
// reaches Silo's login handler with the body it was sent.
func previewAccountLoginGuardWith(sharedAccount func(context.Context, string) (bool, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/v2/auth/login" {
				next.ServeHTTP(w, r)
				return
			}
			raw, err := io.ReadAll(io.LimitReader(r.Body, previewLoginBodyLimit+1))
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(raw), r.Body), r.Body}
			if err != nil || len(raw) > previewLoginBodyLimit {
				next.ServeHTTP(w, r)
				return
			}
			var input struct {
				Username string `json:"username"`
			}
			if json.Unmarshal(raw, &input) != nil || input.Username == "" {
				next.ServeHTTP(w, r)
				return
			}
			shared, err := sharedAccount(r.Context(), input.Username)
			if err != nil {
				previewRefusalLoginUnavailable.write(w, r)
				return
			}
			if shared {
				previewRefusalAccountLogin.write(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
