package jellycompat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/secret"
)

func fixedNow() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

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
	cipher, err := secret.New([]byte("compat-session-scope-test-master-key"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewPersistentSessionStore(24*time.Hour, fixedNow, NewSessionRepository(pool, cipher))
	for _, session := range []Session{
		{Token: "persistent-tenant-a", StreamAppUserID: 42, ProfileID: "profile-a"},
		{Token: "persistent-tenant-b", StreamAppUserID: 42, ProfileID: "profile-b"},
		{Token: "persistent-account", StreamAppUserID: 42},
	} {
		if err := store.Put(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteByUserAndProfileIDs(context.Background(), 42, []string{"profile-a"}); err != nil {
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

func TestDeleteByUserID(t *testing.T) {
	store := NewSessionStore(24*time.Hour, fixedNow)

	// Insert sessions for two different users.
	_ = store.Put(Session{Token: "aaa", StreamAppUserID: 1, Username: "alice"})
	_ = store.Put(Session{Token: "bbb", StreamAppUserID: 1, Username: "alice"})
	_ = store.Put(Session{Token: "ccc", StreamAppUserID: 2, Username: "bob"})

	store.DeleteByUserID(1)

	if _, ok := store.Get("aaa"); ok {
		t.Error("expected session aaa to be deleted")
	}
	if _, ok := store.Get("bbb"); ok {
		t.Error("expected session bbb to be deleted")
	}
	if _, ok := store.Get("ccc"); !ok {
		t.Error("expected session ccc to still exist")
	}
}

func TestGetSlidingWindow_ExtendsWhenBelowHalfTTL(t *testing.T) {
	ttl := 30 * 24 * time.Hour // 30 days
	now := fixedNow()
	clock := func() time.Time { return now }
	store := NewSessionStore(ttl, clock)

	_ = store.Put(Session{Token: "tok1", StreamAppUserID: 1})

	// Advance time to 20 days (past the halfway point of 15 days).
	now = now.Add(20 * 24 * time.Hour)

	session, ok := store.Get("tok1")
	if !ok {
		t.Fatal("expected session to exist")
	}

	// ExpiresAt should be extended to now + ttl.
	expected := now.Add(ttl)
	if !session.ExpiresAt.Equal(expected) {
		t.Errorf("expected ExpiresAt = %v, got %v", expected, session.ExpiresAt)
	}
}

func TestGetSlidingWindow_NoExtensionAboveHalfTTL(t *testing.T) {
	ttl := 30 * 24 * time.Hour
	now := fixedNow()
	clock := func() time.Time { return now }
	store := NewSessionStore(ttl, clock)

	_ = store.Put(Session{Token: "tok2", StreamAppUserID: 1})
	originalExpiry := now.Add(ttl)

	// Advance time to 10 days (before the halfway point of 15 days).
	now = now.Add(10 * 24 * time.Hour)

	session, ok := store.Get("tok2")
	if !ok {
		t.Fatal("expected session to exist")
	}

	// ExpiresAt should NOT have changed.
	if !session.ExpiresAt.Equal(originalExpiry) {
		t.Errorf("expected ExpiresAt = %v, got %v", originalExpiry, session.ExpiresAt)
	}
}

func TestGet_ExpiredSession_ReturnsNotFound(t *testing.T) {
	now := fixedNow()
	clock := func() time.Time { return now }
	store := NewSessionStore(1*time.Hour, clock)

	_ = store.Put(Session{Token: "short-lived", StreamAppUserID: 1})

	// Advance past TTL.
	now = now.Add(2 * time.Hour)

	if _, ok := store.Get("short-lived"); ok {
		t.Error("expected expired session to not be returned")
	}
}
