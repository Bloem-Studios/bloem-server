package auth

import (
	"testing"

	"github.com/google/uuid"
)

// Removing account_incarnation_id from the repository projection would make
// authenticated lifecycle replays fall back to the ABA-unsafe numeric ID.
func TestUserRepositoryReturnsDatabaseAccountIncarnation(t *testing.T) {
	ctx, pool, suffix := newAccessGroupUserRepoDBTest(t)
	users := NewUserRepository(pool)

	created, err := users.Create(ctx, createAuthAccessGroupUserInput(suffix, "incarnation", nil))
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.AccountIncarnationID == uuid.Nil {
		t.Fatal("Create() returned nil account incarnation")
	}

	loaded, err := users.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if loaded.AccountIncarnationID != created.AccountIncarnationID {
		t.Fatalf("GetByID() incarnation = %s, want %s", loaded.AccountIncarnationID, created.AccountIncarnationID)
	}
}
