package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Creating an account publishes it through the managed-tracking clock row.
// A repeatable-read writer whose snapshot predates that publication is fenced
// with SQLSTATE 40001 before it can claim anything; the lifecycle store reruns
// it, and the fresh snapshot sees the claim as an ordinary duplicate.
func TestBloemManagedPublicationFencesRepeatableReadClaims(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	prefix := fmt.Sprintf("managed-fence-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), "DELETE FROM users WHERE username LIKE $1 OR email LIKE $1", prefix+"%")
	})
	users := NewUserRepository(pool)
	input := func(username, email string) models.CreateUserInput {
		return models.CreateUserInput{Username: username, Email: email, Password: "test-password", Role: "user"}
	}
	// claimAfterSnapshot pins tx's snapshot, then commits another account that
	// holds identifier, then lets tx claim identifier as a username.
	claimAfterSnapshot := func(t *testing.T, tx pgx.Tx, owner, identifier string) error {
		t.Helper()
		if _, err := tx.Exec(ctx, "SELECT count(*) FROM users"); err != nil {
			t.Fatal(err)
		}
		if _, err := users.Create(ctx, input(owner, identifier)); err != nil {
			t.Fatal(err)
		}
		_, err := createUser(ctx, tx, input(identifier, owner+"-other@example.invalid"))
		return err
	}

	t.Run("an account published after the snapshot fences the writer", func(t *testing.T) {
		identifier := prefix + "-ivan@example.invalid"
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		err = claimAfterSnapshot(t, tx, prefix+"-owner1", identifier)
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "40001" {
			t.Fatalf("err = %v, want SQLSTATE 40001 from the managed-tracking fence", err)
		}
		_ = tx.Rollback(ctx)
		var holders int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM user_login_identifiers WHERE identifier=$1", identifier).Scan(&holders); err != nil {
			t.Fatal(err)
		}
		if holders != 1 {
			t.Fatalf("identifier holders = %d, want only the published account", holders)
		}
	})

	t.Run("the lifecycle rerun reports the claim as a duplicate", func(t *testing.T) {
		identifier := prefix + "-judy@example.invalid"
		attempts := 0
		err := lifecycleidempotency.NewPostgresStore(pool).InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
			attempts++
			if attempts == 1 {
				return claimAfterSnapshot(t, tx, prefix+"-owner2", identifier)
			}
			_, err := createUser(ctx, tx, input(identifier, prefix+"-owner2-other@example.invalid"))
			return err
		})
		if !IsDuplicate(err) || attempts != 2 {
			t.Fatalf("err = %v after %d attempts, want ErrDuplicate from the second attempt", err, attempts)
		}
	})
}
