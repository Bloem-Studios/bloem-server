package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/models"
)

type fixedGuestSettings struct{ s PreviewGuestSettings }

func (f *fixedGuestSettings) PreviewGuestSettings(context.Context) (PreviewGuestSettings, error) {
	return f.s, nil
}

func guestFixture(t *testing.T) (context.Context, *pgxpool.Pool, *Service, *PreviewGuests, *fixedGuestSettings, int) {
	t.Helper()
	ctx := context.Background()
	pool := NewBloemAuthTestDatabase(t)
	user, err := NewUserRepository(pool).Create(ctx, models.CreateUserInput{Username: "preview-shared", Email: "preview-shared@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE bloem_preview_settings SET account_id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	settings := &fixedGuestSettings{PreviewGuestSettings{Enabled: true, Host: "browse.example.test", AccountID: user.ID}}
	jwt := NewJWTService("guest-test-secret", time.Hour, 24*time.Hour)
	service := NewService(nil, jwt, NewSessionRepository(pool), NewUserRepository(pool), nil, nil, nil)
	guests := service.EnablePreviewGuests(pool, settings)
	return ctx, pool, service, guests, settings, user.ID
}

func openGuest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, guests *PreviewGuests, account int, expires time.Time) *TokenPair {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	pair, err := guests.CreateInTx(ctx, tx, NewPreviewGuest{AccountID: account, ExpiresAt: expires, IdleTTL: 20 * time.Minute, ClientBucket: "bucket-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return pair
}

func TestGuestSessionIsAnonymousAndShortLived(t *testing.T) {
	ctx, pool, _, guests, _, account := guestFixture(t)
	expires := time.Now().Add(2 * time.Hour)
	pair := openGuest(t, ctx, pool, guests, account, expires)
	if pair.ExpiresIn != int(PreviewGuestAccessTTL/time.Second) {
		t.Fatalf("access lifetime = %ds, want 15 min", pair.ExpiresIn)
	}
	claims, err := guests.jwt.ValidateToken(pair.AccessToken)
	if err != nil || claims.AuthMethod != AuthMethodPreviewGuest || claims.UserID != account {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
	var ip, deviceName, method string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(host(ip_address), ''), COALESCE(device_name, ''), auth_method FROM auth_sessions WHERE id=$1`, pair.SessionID).Scan(&ip, &deviceName, &method); err != nil {
		t.Fatal(err)
	}
	if ip != "" || deviceName != "" || method != AuthMethodPreviewGuest {
		t.Fatalf("stored ip=%q device=%q method=%q", ip, deviceName, method)
	}
	// Near the absolute expiry, the access token is capped by it.
	short := openGuest(t, ctx, pool, guests, account, time.Now().Add(3*time.Minute))
	if short.ExpiresIn > 180 {
		t.Fatalf("access lifetime %ds outlives the session", short.ExpiresIn)
	}
}

func TestGuestRefreshNeverSlidesAndHonoursHostAndKillSwitch(t *testing.T) {
	ctx, pool, service, guests, settings, account := guestFixture(t)
	expires := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	pair := openGuest(t, ctx, pool, guests, account, expires)
	onPreview := WithRequestHost(ctx, "browse.example.test")

	if _, err := service.Refresh(onPreview, pair.RefreshToken); err != nil {
		t.Fatalf("refresh on the preview host: %v", err)
	}
	var stored time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM auth_sessions WHERE id=$1`, pair.SessionID).Scan(&stored); err != nil || !stored.Equal(expires) {
		t.Fatalf("expires_at = %v, want unchanged %v (%v)", stored, expires, err)
	}
	if _, err := service.Refresh(WithRequestHost(ctx, "lib.example.test"), pair.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("refresh on another host: %v", err)
	}
	if _, err := service.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("refresh with no host: %v", err)
	}
	settings.s.Enabled = false
	if _, err := service.Refresh(onPreview, pair.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("refresh after the kill switch: %v", err)
	}
}

func TestGuestRefreshRefusesAnIdleSession(t *testing.T) {
	ctx, pool, service, guests, _, account := guestFixture(t)
	pair := openGuest(t, ctx, pool, guests, account, time.Now().Add(2*time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE auth_sessions SET last_seen_at = now() - interval '21 minutes', created_at = now() - interval '30 minutes' WHERE id=$1`, pair.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(WithRequestHost(ctx, "browse.example.test"), pair.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("idle refresh: %v", err)
	}
}

func TestReissueReusesALiveGuestSession(t *testing.T) {
	ctx, pool, _, guests, _, account := guestFixture(t)
	pair := openGuest(t, ctx, pool, guests, account, time.Now().Add(2*time.Hour))
	again, err := guests.Reissue(WithRequestHost(ctx, "browse.example.test"), pair.RefreshToken)
	if err != nil || again.SessionID != pair.SessionID {
		t.Fatalf("reissue = %+v, %v", again, err)
	}
	if _, err := guests.Reissue(WithRequestHost(ctx, "browse.example.test"), "not-a-token"); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("garbage refresh token: %v", err)
	}
}
