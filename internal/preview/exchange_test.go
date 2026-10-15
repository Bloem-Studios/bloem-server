package preview_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/preview"
)

type staticServerID string

func (s staticServerID) Resolve(context.Context) (string, error) { return string(s), nil }

type exchangeFixture struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	store    *preview.Store
	priv     ed25519.PrivateKey
	settings preview.Settings
	ex       *preview.Exchanger
}

func newExchangeFixture(t *testing.T, maxConcurrent int) *exchangeFixture {
	t.Helper()
	ctx, pool := previewTestDB(t)
	if _, err := pool.Exec(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE auth_method='preview_guest' AND revoked_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	store := preview.NewStore(pool)
	pub, priv := newKey(t)
	settings := validSettings(t, insertAccount(t, ctx, pool, "user"), pub)
	settings.Issuer, settings.MaxConcurrent = want.Issuer, maxConcurrent
	if err := store.Put(ctx, settings); err != nil {
		t.Fatal(err)
	}
	reader := preview.NewReader(store, 0)
	guests := auth.NewPreviewGuests(pool, auth.NewJWTService("exchange-secret", time.Hour, time.Hour), reader)
	return &exchangeFixture{ctx: auth.WithRequestHost(ctx, settings.Host), pool: pool, store: store, priv: priv, settings: settings,
		ex: preview.NewExchanger(pool, guests, staticServerID(want.Audience), preview.NewClientBuckets())}
}

func (f *exchangeFixture) ticket(t *testing.T) string {
	now := time.Now()
	return sign(t, f.priv, jwt.MapClaims{"iss": want.Issuer, "aud": want.Audience, "host": f.settings.Host,
		"jti": uuid.NewString()[:8] + uuid.NewString()[:8] + uuid.NewString()[:8], "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "item": "movie:7"})
}

func (f *exchangeFixture) exchange(t *testing.T, ip, refresh string) (preview.ExchangeResult, error) {
	return f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: f.ticket(t), RefreshToken: refresh, Host: f.settings.Host, ClientIP: ip})
}

func TestExchangeOpensAGuestSession(t *testing.T) {
	f := newExchangeFixture(t, 200)
	res, err := f.exchange(t, "198.51.100.7", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Item != "movie:7" || res.Tokens == nil || res.Reused || res.ExpiresAt.Before(time.Now().Add(time.Hour)) {
		t.Fatalf("result = %+v", res)
	}
	var bucket string
	if err := f.pool.QueryRow(f.ctx, `SELECT client_bucket FROM bloem_preview_guest_sessions WHERE session_id=$1`, res.Tokens.SessionID).Scan(&bucket); err != nil {
		t.Fatal(err)
	}
	if bucket == "" || bucket == "198.51.100.7" {
		t.Fatalf("client bucket %q stores the address", bucket)
	}
}

func TestExchangeRefusesAReplayAndAWrongHostAndADisabledPreview(t *testing.T) {
	f := newExchangeFixture(t, 200)
	raw := f.ticket(t)
	if _, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: raw, Host: f.settings.Host, ClientIP: "198.51.100.8"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: raw, Host: f.settings.Host, ClientIP: "198.51.100.8"}); !errors.Is(err, preview.ErrTicketInvalid) {
		t.Fatalf("replay: %v", err)
	}
	if _, err := f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: f.ticket(t), Host: "lib.example.test", ClientIP: "198.51.100.8"}); !errors.Is(err, preview.ErrUnavailable) {
		t.Fatalf("wrong host: %v", err)
	}
	if err := f.store.Disable(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.exchange(t, "198.51.100.8", ""); !errors.Is(err, preview.ErrUnavailable) {
		t.Fatalf("disabled: %v", err)
	}
}

func TestExchangeReusesALiveSessionFromTheSameBrowser(t *testing.T) {
	f := newExchangeFixture(t, 200)
	first, err := f.exchange(t, "198.51.100.9", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.exchange(t, "198.51.100.9", first.Tokens.RefreshToken)
	if err != nil || !second.Reused || second.Tokens.SessionID != first.Tokens.SessionID || second.Item != "movie:7" {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestExchangeEvictsAClientsOldestBeyondThree(t *testing.T) {
	f := newExchangeFixture(t, 200)
	var ids []string
	for range 4 {
		res, err := f.exchange(t, "2001:db8:1:2::5", "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.Tokens.SessionID)
	}
	// Same /64, different interface id: the same client.
	if _, err := f.exchange(t, "2001:db8:1:2::99", ""); err != nil {
		t.Fatal(err)
	}
	var live int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM auth_sessions s JOIN bloem_preview_guest_sessions g ON g.session_id=s.id
		WHERE s.revoked_at IS NULL AND g.client_bucket = (SELECT client_bucket FROM bloem_preview_guest_sessions WHERE session_id=$1)`, ids[0]).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != preview.MaxGuestsPerClient {
		t.Fatalf("live sessions for one client = %d, want %d", live, preview.MaxGuestsPerClient)
	}
	var firstRevoked bool
	if err := f.pool.QueryRow(f.ctx, `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id=$1`, ids[0]).Scan(&firstRevoked); err != nil || !firstRevoked {
		t.Fatalf("oldest session not evicted: %v %v", firstRevoked, err)
	}
}

func TestExchangeCapacityIsSerialized(t *testing.T) {
	f := newExchangeFixture(t, 1)
	tickets := []string{f.ticket(t), f.ticket(t), f.ticket(t), f.ticket(t)}
	var wg sync.WaitGroup
	results := make([]error, len(tickets))
	for i, raw := range tickets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = f.ex.Exchange(f.ctx, preview.ExchangeInput{Ticket: raw, Host: f.settings.Host, ClientIP: "203.0.113." + string(rune('1'+i))})
		}()
	}
	wg.Wait()
	won, busy := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, preview.ErrBusy):
			busy++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if won != 1 || busy != 3 {
		t.Fatalf("won=%d busy=%d, want 1 and 3", won, busy)
	}
}

func TestClientBucketsGroupBySlash64AndHideTheAddress(t *testing.T) {
	b := preview.NewClientBuckets()
	if b.Key("2001:db8::1") != b.Key("2001:db8::ffff") {
		t.Fatal("one /64 produced two buckets")
	}
	if b.Key("2001:db8:0:1::1") == b.Key("2001:db8::1") {
		t.Fatal("two /64s shared a bucket")
	}
	if b.Key("198.51.100.1") == b.Key("198.51.100.2") {
		t.Fatal("two IPv4 addresses shared a bucket")
	}
	if preview.NewClientBuckets().Key("198.51.100.1") == b.Key("198.51.100.1") {
		t.Fatal("buckets are linkable across processes")
	}
}

func TestHourlyLimiterAllowsTwentyAnHour(t *testing.T) {
	l := preview.NewHourlyLimiter(20)
	for i := range 20 {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("request %d refused", i+1)
		}
	}
	if ok, wait := l.Allow("k"); ok || wait <= 0 {
		t.Fatalf("21st allowed=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("another client was limited")
	}
}

func TestExchangeClientLimitAcrossServerProcesses(t *testing.T) {
	f := newExchangeFixture(t, 200)
	issuer := auth.NewPreviewGuests(f.pool, auth.NewJWTService("exchange-secret", time.Hour, time.Hour), preview.NewReader(f.store, 0))
	nodes := []*preview.Exchanger{
		preview.NewExchanger(f.pool, issuer, staticServerID(want.Audience), preview.NewClientBucketsWithSecret("shared-server-secret")),
		preview.NewExchanger(f.pool, issuer, staticServerID(want.Audience), preview.NewClientBucketsWithSecret("shared-server-secret")),
	}
	var first string
	for i := range 4 {
		res, err := nodes[i%2].Exchange(f.ctx, preview.ExchangeInput{Ticket: f.ticket(t), Host: f.settings.Host, ClientIP: "198.51.100.42"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = res.Tokens.SessionID
		}
	}
	var live int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM auth_sessions WHERE auth_method='preview_guest' AND revoked_at IS NULL`).Scan(&live); err != nil || live != 3 {
		t.Fatalf("live=%d err=%v; want3 across nodes", live, err)
	}
	var revoked bool
	if err := f.pool.QueryRow(f.ctx, `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id=$1`, first).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("oldest revoked=%v err=%v", revoked, err)
	}
}

func TestExchangeReusesWithOneDatabaseConnection(t *testing.T) {
	f := newExchangeFixture(t, 200)
	cfg := f.pool.Config()
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	reader := preview.NewReader(preview.NewStore(one), 0)
	issuer := auth.NewPreviewGuests(one, auth.NewJWTService("exchange-secret", time.Hour, time.Hour), reader)
	ex := preview.NewExchanger(one, issuer, staticServerID(want.Audience), preview.NewClientBuckets())
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	first, err := ex.Exchange(ctx, preview.ExchangeInput{Ticket: f.ticket(t), Host: f.settings.Host, ClientIP: "198.51.100.43"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ex.Exchange(ctx, preview.ExchangeInput{Ticket: f.ticket(t), Host: f.settings.Host, ClientIP: "198.51.100.43", RefreshToken: first.Tokens.RefreshToken})
	if err != nil || !second.Reused {
		t.Fatalf("reuse with one connection: reused=%v err=%v", second.Reused, err)
	}
}
