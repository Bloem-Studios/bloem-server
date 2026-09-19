package jellycompat

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/secret"
)

func TestDeleteByUserAndProfileIDsPreservesOtherTenantAndAccountSessions(t *testing.T) {
	store := NewSessionStore(24*time.Hour, fixedNow)
	for _, session := range []Session{
		{Token: "tenant-a", StreamAppUserID: 1, ProfileID: "profile-a"},
		{Token: "tenant-b", StreamAppUserID: 1, ProfileID: "profile-b"},
		{Token: "account", StreamAppUserID: 1},
		{Token: "other-user", StreamAppUserID: 2, ProfileID: "profile-a"},
	} {
		if err := store.Put(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteByUserAndProfileIDs(context.Background(), 1, []string{"profile-a"}); err != nil {
		t.Fatalf("delete scoped sessions: %v", err)
	}
	if _, ok := store.Get("tenant-a"); ok {
		t.Fatal("tenant A session remained")
	}
	for _, token := range []string{"tenant-b", "account", "other-user"} {
		if _, ok := store.Get(token); !ok {
			t.Fatalf("scoped delete removed %q", token)
		}
	}
}

type failingUserDeletePersistence struct {
	sessions map[string]Session
	err      error
}

func (p *failingUserDeletePersistence) Upsert(_ context.Context, session Session) error {
	p.sessions[session.Token] = session
	return nil
}
func (p *failingUserDeletePersistence) GetByToken(_ context.Context, token string, _ time.Time) (*Session, error) {
	session, ok := p.sessions[token]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return &session, nil
}
func (p *failingUserDeletePersistence) DeleteByToken(_ context.Context, token string) error {
	delete(p.sessions, token)
	return nil
}
func (p *failingUserDeletePersistence) DeleteByUserID(context.Context, int) (int, error) {
	return 0, p.err
}

func TestDeleteByUserIDContextLeavesMemoryIntactWhenPersistenceFails(t *testing.T) {
	wantErr := errors.New("persistent delete failed")
	repo := &failingUserDeletePersistence{sessions: make(map[string]Session), err: wantErr}
	store := NewPersistentSessionStore(24*time.Hour, fixedNow, repo)
	if err := store.Put(Session{Token: "still-valid", StreamAppUserID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteByUserIDContext(context.Background(), 1); !errors.Is(err, wantErr) {
		t.Fatalf("delete error = %v, want %v", err, wantErr)
	}
	if _, ok := store.Get("still-valid"); !ok {
		t.Fatal("failed durable eviction removed only the in-memory copy")
	}
}

func TestDeleteByUserAndProfileIDsFailsClosedForUnsupportedPersistence(t *testing.T) {
	repo := &failingUserDeletePersistence{sessions: make(map[string]Session)}
	store := NewPersistentSessionStore(24*time.Hour, fixedNow, repo)
	if err := store.Put(Session{Token: "still-durable", StreamAppUserID: 1, ProfileID: "profile-a"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteByUserAndProfileIDs(context.Background(), 1, []string{"profile-a"}); err == nil {
		t.Fatal("scoped delete succeeded without durable persistence support")
	}
	if _, ok := store.Get("still-durable"); !ok {
		t.Fatal("unsupported durable eviction removed only the in-memory copy")
	}
}

func TestPersistentDeleteByUserAndProfileIDsIsTenantScoped(t *testing.T) {
	pool := newCompatIdentityDatabase(t)
	account, err := auth.NewUserRepository(pool).Create(context.Background(), models.CreateUserInput{
		Username: "scoped-session-account",
		Email:    "scoped-session-account@example.test",
		Password: "fixture-session-account-password",
		Role:     "user",
	})
	if err != nil {
		t.Fatalf("create session account: %v", err)
	}
	cipher, err := secret.New([]byte("compat-session-scope-test-master-key"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPersistentSessionStore(24*time.Hour, fixedNow, NewSessionRepository(pool, cipher))
	for _, session := range []Session{
		{Token: "persistent-tenant-a", StreamAppUserID: account.ID, ProfileID: "profile-a"},
		{Token: "persistent-tenant-b", StreamAppUserID: account.ID, ProfileID: "profile-b"},
		{Token: "persistent-account", StreamAppUserID: account.ID},
	} {
		if err := store.Put(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteByUserAndProfileIDs(context.Background(), account.ID, []string{"profile-a"}); err != nil {
		t.Fatal(err)
	}
	var deletedA, retainedB, retainedAccount bool
	if err := pool.QueryRow(context.Background(), `SELECT NOT EXISTS(SELECT 1 FROM jellycompat_sessions WHERE token='persistent-tenant-a')`).Scan(&deletedA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM jellycompat_sessions WHERE token='persistent-tenant-b')`).Scan(&retainedB); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM jellycompat_sessions WHERE token='persistent-account')`).Scan(&retainedAccount); err != nil {
		t.Fatal(err)
	}
	if !deletedA || !retainedB || !retainedAccount {
		t.Fatalf("persistent scoped delete: deleted_a=%v retained_b=%v retained_account=%v", deletedA, retainedB, retainedAccount)
	}
}
