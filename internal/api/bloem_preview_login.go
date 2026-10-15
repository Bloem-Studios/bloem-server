package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Reject shared-account password sign-in before the ordinary handler can
// authenticate it and turn the database credential guard into a 500.
func previewAccountLoginGuard(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || (r.URL.Path != "/api/v1/auth/login" && r.URL.Path != "/api/v2/auth/login") {
				next.ServeHTTP(w, r)
				return
			}
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
			if err != nil {
				http.Error(w, "Invalid login request", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var input struct {
				Username string `json:"username"`
			}
			if json.Unmarshal(raw, &input) != nil || input.Username == "" {
				next.ServeHTTP(w, r)
				return
			}
			var shared bool
			err = pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM bloem_preview_settings s JOIN user_login_identifiers i ON i.user_id=s.account_id WHERE i.identifier=$1)`, input.Username).Scan(&shared)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"unavailable","message":"Sign-in is unavailable."}`))
				return
			}
			if shared {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"forbidden","code":"preview_account","message":"Use a preview launch link to browse."}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
