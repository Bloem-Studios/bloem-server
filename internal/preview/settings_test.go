package preview_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/preview"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func validSettings(t *testing.T, accountID int, key ed25519.PublicKey) preview.Settings {
	return preview.Settings{
		Enabled: true, Host: "browse.example.test", Issuer: "garden-operator-1", AccountID: accountID,
		TicketPublicKeys: []ed25519.PublicKey{key}, SessionTTL: 2 * time.Hour, IdleTTL: 20 * time.Minute,
		MaxConcurrent: 200, Brand: "Example", CTALabel: "Subscribe to Example",
	}
}

func TestStoreRoundTripsSettings(t *testing.T) {
	ctx, pool := previewTestDB(t)
	store := preview.NewStore(pool)
	pub, _ := newKey(t)
	want := validSettings(t, insertAccount(t, ctx, pool, "user"), pub)
	if err := store.Put(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != want.Host || got.AccountID != want.AccountID || !got.Enabled || len(got.TicketPublicKeys) != 1 ||
		!got.TicketPublicKeys[0].Equal(pub) || got.IdleTTL != want.IdleTTL || got.SessionTTL != want.SessionTTL {
		t.Fatalf("got %+v", got)
	}
}

func TestPutRefusesInvalidSettings(t *testing.T) {
	ctx, pool := previewTestDB(t)
	store := preview.NewStore(pool)
	pub, _ := newKey(t)
	account := insertAccount(t, ctx, pool, "user")
	admin := insertAccount(t, ctx, pool, "admin")
	for name, mutate := range map[string]func(*preview.Settings){
		"host with scheme": func(s *preview.Settings) { s.Host = "https://browse.example.test" },
		"host with port":   func(s *preview.Settings) { s.Host = "browse.example.test:443" },
		"host with path":   func(s *preview.Settings) { s.Host = "browse.example.test/x" },
		"upper-case host":  func(s *preview.Settings) { s.Host = "Browse.Example.Test" },
		"admin account":    func(s *preview.Settings) { s.AccountID = admin },
		"missing account":  func(s *preview.Settings) { s.AccountID = 0 },
		"three keys":       func(s *preview.Settings) { s.TicketPublicKeys = []ed25519.PublicKey{pub, pub, pub} },
		"no keys":          func(s *preview.Settings) { s.TicketPublicKeys = nil },
		"short key":        func(s *preview.Settings) { s.TicketPublicKeys = []ed25519.PublicKey{pub[:16]} },
		"empty issuer":     func(s *preview.Settings) { s.Issuer = "" },
		"idle beyond ttl":  func(s *preview.Settings) { s.IdleTTL = 3 * time.Hour },
		"zero capacity":    func(s *preview.Settings) { s.MaxConcurrent = 0 },
		"long cta_label":   func(s *preview.Settings) { s.CTALabel = strings.Repeat("x", 121) },
	} {
		s := validSettings(t, account, pub)
		mutate(&s)
		if err := store.Put(ctx, s); !errors.Is(err, preview.ErrInvalidSettings) {
			t.Errorf("%s: err = %v, want ErrInvalidSettings", name, err)
		}
	}
	// A disabled, unconfigured preview is valid.
	if err := store.Put(ctx, preview.Settings{SessionTTL: 2 * time.Hour, IdleTTL: 20 * time.Minute, MaxConcurrent: 200}); err != nil {
		t.Fatalf("disabled preview: %v", err)
	}
}

func TestPutRevokesTheNewSharedAccountsCredentials(t *testing.T) {
	ctx, pool := previewTestDB(t)
	store := preview.NewStore(pool)
	pub, _ := newKey(t)
	account := insertAccount(t, ctx, pool, "user")
	sessionID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO auth_sessions (id, user_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')`, sessionID, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO api_keys (user_id, label, api_key) VALUES ($1, 'old', $2)`, account, "sa_"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, validSettings(t, account, pub)); err != nil {
		t.Fatal(err)
	}
	var revoked bool
	var keys int
	if err := pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id=$1`, sessionID).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("existing session revoked = %v (%v)", revoked, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE user_id=$1`, account).Scan(&keys); err != nil || keys != 0 {
		t.Fatalf("api keys left = %d (%v)", keys, err)
	}
}

func TestDisableRevokesEveryGuest(t *testing.T) {
	ctx, pool := previewTestDB(t)
	store := preview.NewStore(pool)
	pub, _ := newKey(t)
	account := insertAccount(t, ctx, pool, "user")
	if err := store.Put(ctx, validSettings(t, account, pub)); err != nil {
		t.Fatal(err)
	}
	guest := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO auth_sessions (id, user_id, expires_at, auth_method) VALUES ($1, $2, now() + interval '1 hour', 'preview_guest')`, guest, account); err != nil {
		t.Fatal(err)
	}
	if err := store.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	var revoked bool
	if err := pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id=$1`, guest).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("guest revoked = %v (%v)", revoked, err)
	}
	got, _ := store.Get(ctx)
	if got.Enabled {
		t.Fatal("still enabled")
	}
}

func TestReaderCachesAndReportsGuestSettings(t *testing.T) {
	ctx, pool := previewTestDB(t)
	store := preview.NewStore(pool)
	pub, _ := newKey(t)
	account := insertAccount(t, ctx, pool, "user")
	if err := store.Put(ctx, validSettings(t, account, pub)); err != nil {
		t.Fatal(err)
	}
	reader := preview.NewReader(store, time.Hour)
	guest, err := reader.PreviewGuestSettings(ctx)
	if err != nil || !guest.Enabled || guest.Host != "browse.example.test" || guest.AccountID != account {
		t.Fatalf("guest settings = %+v, %v", guest, err)
	}
	// Disable reaches the reader that made the change at once, despite the cache.
	if err := store.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	reader.Invalidate()
	if guest, _ := reader.PreviewGuestSettings(ctx); guest.Enabled {
		t.Fatal("reader kept a disabled preview after Invalidate")
	}
}

func TestGuestAdmissionReadsFreshSettings(t *testing.T) {
	ctx, pool := previewTestDB(t)
	store := preview.NewStore(pool)
	pub, _ := newKey(t)
	s := validSettings(t, insertAccount(t, ctx, pool, "user"), pub)
	if err := store.Put(ctx, s); err != nil {
		t.Fatal(err)
	}
	reader := preview.NewReader(store, time.Hour)
	if _, err := reader.PreviewGuestSettings(ctx); err != nil {
		t.Fatal(err)
	}
	s.Host = "new-browse.example.test"
	if err := store.Put(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := reader.PreviewGuestSettings(ctx)
	if err != nil || got.Host != s.Host {
		t.Fatalf("guest admission retained old host: %+v %v", got, err)
	}
	if err := store.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = reader.PreviewGuestSettings(ctx)
	if err != nil || got.Enabled {
		t.Fatalf("guest admission retained enabled state: %+v %v", got, err)
	}
}
