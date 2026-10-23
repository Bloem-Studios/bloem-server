package preview_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/migrations"
)

func previewTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
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
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	// Each test starts from an unconfigured preview.
	if _, err := pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=false, host='', issuer='', account_id=NULL, ticket_public_keys='{}'`); err != nil {
		t.Fatal(err)
	}
	return ctx, pool
}

func insertAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role string) int {
	t.Helper()
	name := "preview-" + uuid.NewString()[:8]
	var id int
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ($1::text, $1::text || '@example.test', '', $2, true) RETURNING id`, name, role).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func requireSQLState(t *testing.T, err error, state string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != state {
		t.Fatalf("err = %v, want SQLSTATE %s", err, state)
	}
}

func TestSharedAccountHoldsOnlyGuestSessions(t *testing.T) {
	ctx, pool := previewTestDB(t)
	shared := insertAccount(t, ctx, pool, "user")
	other := insertAccount(t, ctx, pool, "user")
	if _, err := pool.Exec(ctx, `UPDATE bloem_preview_settings SET account_id=$1`, shared); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)

	_, err := pool.Exec(ctx, `INSERT INTO auth_sessions (id, user_id, expires_at) VALUES ($1, $2, $3)`, uuid.NewString(), shared, expires)
	requireSQLState(t, err, "42501")
	_, err = pool.Exec(ctx, `INSERT INTO api_keys (user_id, label, key_hash) VALUES ($1, 'x', encode(sha256(convert_to($2, 'UTF8')), 'hex'))`, shared, "sa_"+uuid.NewString())
	requireSQLState(t, err, "42501")
	_, err = pool.Exec(ctx, `INSERT INTO auth_sessions (id, user_id, expires_at, auth_method) VALUES ($1, $2, $3, 'preview_guest')`, uuid.NewString(), other, expires)
	requireSQLState(t, err, "42501")

	if _, err := pool.Exec(ctx, `INSERT INTO auth_sessions (id, user_id, expires_at, auth_method) VALUES ($1, $2, $3, 'preview_guest')`, uuid.NewString(), shared, expires); err != nil {
		t.Fatalf("guest session on the shared account: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO auth_sessions (id, user_id, expires_at) VALUES ($1, $2, $3)`, uuid.NewString(), other, expires); err != nil {
		t.Fatalf("ordinary session on another account: %v", err)
	}
}

func TestEnabledPreviewSettingsMustBeComplete(t *testing.T) {
	ctx, pool := previewTestDB(t)
	_, err := pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=true`)
	requireSQLState(t, err, "23514")
}

func TestSharedAccountGuardSerializesCredentialInsertWithAssignment(t *testing.T) {
	for _, kind := range []string{"session", "api-key"} {
		t.Run(kind, func(t *testing.T) {
			ctx, pool := previewTestDB(t)
			account := insertAccount(t, ctx, pool, "user")
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, account); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `UPDATE bloem_preview_settings SET account_id=$1`, account); err != nil {
				t.Fatal(err)
			}
			cfg := pool.Config().ConnConfig.Copy()
			conn, err := pgx.ConnectConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close(ctx) }()
			done := make(chan error, 1)
			go func() {
				var err error
				if kind == "session" {
					_, err = conn.Exec(ctx, `INSERT INTO auth_sessions (id,user_id,expires_at) VALUES ($1,$2,now()+interval '1 hour')`, uuid.NewString(), account)
				} else {
					_, err = conn.Exec(ctx, `INSERT INTO api_keys (user_id, label, key_hash) VALUES ($1, 'race', encode(sha256(convert_to($2, 'UTF8')), 'hex'))`, account, "sa_"+uuid.NewString())
				}
				done <- err
			}()
			deadline := time.Now().Add(3 * time.Second)
			for {
				var blocked bool
				if err := pool.QueryRow(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, conn.PgConn().PID()).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("credential insert did not reach its row lock")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				requireSQLState(t, err, "42501")
			case <-time.After(3 * time.Second):
				t.Fatal("credential insert did not finish")
			}
		})
	}
}
