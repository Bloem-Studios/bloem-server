package jellycompat

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"
	"github.com/google/uuid"
)

func newBloemRevocationRepository(t *testing.T) (*SessionRepository, int, int, string) {
	t.Helper()
	pool, suffix := jellyfin12CompatPool(t)
	ctx := context.Background()
	users := auth.NewUserRepository(pool)
	ids := make([]int, 2)
	for i := range ids {
		user, err := users.Create(ctx, models.CreateUserInput{
			Username: fmt.Sprintf("compat-revoke-%d-%d", suffix, i),
			Email:    fmt.Sprintf("compat-revoke-%d-%d@example.test", suffix, i),
			Password: "fixture-compat-revocation-password", Role: models.RoleUser,
		})
		if err != nil {
			t.Fatalf("create revocation user: %v", err)
		}
		ids[i] = user.ID
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID) })
	}
	cipher, err := secret.New([]byte("bloem-compat-revocation-fixture-key"))
	if err != nil {
		t.Fatal(err)
	}
	return NewSessionRepository(pool, cipher), ids[0], ids[1], fmt.Sprintf("compat-revoke-%d", suffix)
}

// The production callback deletes durable compat rows before evicting the
// local store and publishing account eviction to the other replicas. Use the
// real repository here: a memory-only store hid its missing deletion method.
func TestPersistentUserRevocationReachesReplicaEvictionAfterCancellationDB(t *testing.T) {
	repo, userID, otherUserID, prefix := newBloemRevocationRepository(t)
	local := NewPersistentSessionStore(24*time.Hour, fixedNow, repo)
	replica := NewPersistentSessionStore(24*time.Hour, fixedNow, repo)
	sessions := []Session{
		{Token: prefix + "-account", StreamAppUserID: userID},
		{Token: prefix + "-profile", StreamAppUserID: userID, ProfileID: "profile-a"},
		{Token: prefix + "-other-profile", StreamAppUserID: userID, ProfileID: "profile-b"},
		{Token: prefix + "-other-account", StreamAppUserID: otherUserID},
	}
	for _, session := range sessions {
		session.PseudoUserID = uuid.New()
		if err := local.Put(session); err != nil {
			t.Fatal(err)
		}
		if _, err := replica.Lookup(t.Context(), session.Token); err != nil {
			t.Fatalf("warm replica: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // The account mutation committed before the caller disconnected.
	published := false
	err := sessioninvalidation.Run(ctx, func(callCtx context.Context) error {
		if err := local.DeleteByUserIDContext(callCtx, userID); err != nil {
			return err
		}
		// Receiving the admin event uses EvictUser, without another DB delete.
		published = true
		replica.EvictUser(userID)
		return nil
	})
	if err != nil {
		t.Fatalf("post-commit persistent revocation: %v", err)
	}
	if !published {
		t.Fatal("durable revocation did not reach replica eviction publication")
	}
	for _, session := range sessions {
		for _, store := range []*SessionStore{local, replica} {
			_, err := store.Lookup(t.Context(), session.Token)
			if session.StreamAppUserID == userID && !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("revoked session remains cached/reloadable: %v", err)
			}
			if session.StreamAppUserID == otherUserID && err != nil {
				t.Fatalf("another account's session was revoked: %v", err)
			}
		}
	}
	// Upstream may already have removed the rows transactionally. Repeating
	// invalidation must still succeed so every replica gets its eviction event.
	if err := local.DeleteByUserIDContext(t.Context(), userID); err != nil {
		t.Fatalf("already deleted account invalidation: %v", err)
	}
}

type revocationDuringSessionLoad struct {
	*SessionRepository
	afterGet func()
}

func (r *revocationDuringSessionLoad) GetByToken(ctx context.Context, token string, now time.Time) (*Session, error) {
	session, err := r.SessionRepository.GetByToken(ctx, token, now)
	if err == nil && r.afterGet != nil {
		callback := r.afterGet
		r.afterGet = nil
		callback()
	}
	return session, err
}

func TestPersistentUserRevocationFencesConcurrentLoadDB(t *testing.T) {
	repo, userID, _, prefix := newBloemRevocationRepository(t)
	if err := repo.Upsert(t.Context(), Session{
		Token: prefix, StreamAppUserID: userID, PseudoUserID: uuid.New(),
		CreatedAt: fixedNow(), ExpiresAt: fixedNow().Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	racing := &revocationDuringSessionLoad{SessionRepository: repo}
	store := NewPersistentSessionStore(24*time.Hour, fixedNow, racing)
	racing.afterGet = func() {
		if err := store.DeleteByUserIDContext(t.Context(), userID); err != nil {
			t.Fatalf("revoke during persistent read: %v", err)
		}
	}
	if _, err := store.Lookup(t.Context(), prefix); err != nil {
		t.Fatalf("read started before revocation: %v", err)
	}
	if _, err := store.Lookup(t.Context(), prefix); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("in-flight read repopulated revoked session: %v", err)
	}
}

func TestPersistentUserRevocationDurableFailureKeepsCacheDB(t *testing.T) {
	repo, userID, _, prefix := newBloemRevocationRepository(t)
	store := NewPersistentSessionStore(24*time.Hour, fixedNow, repo)
	if err := store.Put(Session{Token: prefix, StreamAppUserID: userID, PseudoUserID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.DeleteByUserIDContext(ctx, userID); !errors.Is(err, context.Canceled) {
		t.Fatalf("durable invalidation failure = %v, want context.Canceled", err)
	}
	if _, err := repo.GetByToken(t.Context(), prefix, fixedNow()); err != nil {
		t.Fatalf("failed invalidation deleted durable session: %v", err)
	}
	if _, err := store.Lookup(t.Context(), prefix); err != nil {
		t.Fatalf("failed invalidation evicted only the cached session: %v", err)
	}
	if store.evictions != 0 {
		t.Fatal("failed durable invalidation advanced the eviction fence")
	}
}
