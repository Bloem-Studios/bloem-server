package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

func TestRevokeByUserAndSessionScopesTheMutation(t *testing.T) {
	service := newProfileCredentialService(t)
	userA, _ := newProfileCredentialFixture(t, service.pool, "session-scope-a")
	userB, _ := newProfileCredentialFixture(t, service.pool, "session-scope-b")
	repository := NewSessionRepository(service.pool)
	ctx := context.Background()

	for _, session := range []models.AuthSession{
		{ID: "scope-session-a", UserID: userA, ExpiresAt: time.Now().Add(time.Hour)},
		{ID: "scope-session-b", UserID: userB, ExpiresAt: time.Now().Add(time.Hour)},
	} {
		if err := repository.Create(ctx, session); err != nil {
			t.Fatalf("Create(%q): %v", session.ID, err)
		}
	}

	if err := repository.RevokeByUserAndSession(ctx, userA, "scope-session-b"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("cross-user revoke error = %v, want ErrSessionNotFound", err)
	}
	if valid, err := repository.IsValid(ctx, "scope-session-b"); err != nil || !valid {
		t.Fatalf("foreign session validity = %v, %v; want active", valid, err)
	}

	if err := repository.RevokeByUserAndSession(ctx, userA, "scope-session-a"); err != nil {
		t.Fatalf("owned revoke: %v", err)
	}
	if valid, err := repository.IsValid(ctx, "scope-session-a"); err != nil || valid {
		t.Fatalf("owned session validity = %v, %v; want revoked", valid, err)
	}
}

func TestSessionRepositoryCallerTransactionControlsScopedRevoke(t *testing.T) {
	service := newProfileCredentialService(t)
	userID, profileID := newProfileCredentialFixture(t, service.pool, "session-caller-tx")
	repository := NewSessionRepository(service.pool)
	ctx := context.Background()
	profileRevision := int64(1)
	for _, id := range []string{"caller-tx-one", "caller-tx-two"} {
		profile := profileID
		if err := repository.Create(ctx, models.AuthSession{ID: id, UserID: userID, DeviceID: "device-" + id, ProfileID: &profile, ProfileCredentialRevision: &profileRevision, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin single revoke: %v", err)
	}
	if err := repository.RevokeByUserAndSessionInTransaction(ctx, tx, userID, "caller-tx-one"); err != nil {
		t.Fatalf("revoke one in transaction: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback single revoke: %v", err)
	}
	if valid, err := repository.IsValid(ctx, "caller-tx-one"); err != nil || !valid {
		t.Fatalf("single session after rollback valid=%v, error=%v", valid, err)
	}

	tx, err = service.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin profile revoke: %v", err)
	}
	if err := repository.RevokeAllByUserAndProfilesInTransaction(ctx, tx, userID, []string{profileID}); err != nil {
		t.Fatalf("revoke profiles in transaction: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit profile revoke: %v", err)
	}
	for _, id := range []string{"caller-tx-one", "caller-tx-two"} {
		if valid, err := repository.IsValid(ctx, id); err != nil || valid {
			t.Fatalf("%s after commit valid=%v, error=%v", id, valid, err)
		}
	}
}
