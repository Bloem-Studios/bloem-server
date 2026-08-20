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
