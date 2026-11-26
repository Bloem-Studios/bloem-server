package auth_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloemtestdevice"
	"github.com/Silo-Server/silo-server/internal/models"
)

func bloemRevocationAccount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	name := fmt.Sprintf("device-revocation-%d", time.Now().UnixNano())
	user, err := auth.NewUserRepository(pool).Create(t.Context(), models.CreateUserInput{
		Username: name, Email: name + "@example.test", Password: "device revocation", Role: models.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID) })
	return user.ID
}

// Bloem's account-wide sign-outs withdraw the device sign-ins the account
// approved that no device has collected, as Silo's own revocation does, so
// such a device cannot collect a session afterwards. An approval nothing
// revoked still collects one.
func TestBloemRevocationsWithdrawApprovedDeviceSignInsDB(t *testing.T) {
	pool := auth.NewBloemAuthTestDatabase(t)
	ctx := t.Context()
	sessions := auth.NewSessionRepository(pool)

	control := bloemtestdevice.Approve(t, pool, bloemRevocationAccount(t, pool))
	if poll, err := control.Collect(ctx); err != nil || poll.TokenPair == nil {
		t.Fatalf("an approval nothing revoked = %+v, %v; want a session", poll, err)
	}

	t.Run("sign out everywhere", func(t *testing.T) {
		account := bloemRevocationAccount(t, pool)
		signIn := bloemtestdevice.Approve(t, pool, account)
		if err := sessions.BloemRevokeAllByUser(ctx, account); err != nil {
			t.Fatal(err)
		}
		signIn.RequireWithdrawn(t)
	})
	t.Run("account change", func(t *testing.T) {
		account := bloemRevocationAccount(t, pool)
		signIn := bloemtestdevice.Approve(t, pool, account)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := tx.Exec(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, account); err != nil {
			t.Fatal(err)
		}
		if err := sessions.RevokeAllByUserInTransaction(ctx, tx, account); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		signIn.RequireWithdrawn(t)
	})
	t.Run("missing account", func(t *testing.T) {
		if err := sessions.BloemRevokeAllByUser(ctx, -1); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("revoking a missing account = %v, want ErrNotFound", err)
		}
	})
}
