package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// newDirectProfileService wires a real service over the credential fixture's
// pool so these tests exercise the same repositories production uses.
func newDirectProfileService(t *testing.T, pool *pgxpool.Pool, credentials *ProfileCredentialService) (*Service, *JWTService, *SessionRepository) {
	t.Helper()
	sessions := NewSessionRepository(pool)
	jwt := NewJWTService("direct-profile-secret", time.Hour, 24*time.Hour)
	service := NewService(nil, jwt, sessions, NewUserRepository(pool), NewInviteCodeRepository(pool), nil, nil)
	service.SetProfileCredentialService(credentials)
	return service, jwt, sessions
}

// waitForBlockedBackend waits until a backend is parked on a lock, so the
// racing test can commit its rotation knowing the session insert is already
// queued behind the row lock rather than racing on a sleep.
func waitForBlockedBackend(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database()
			  AND wait_event_type = 'Lock'
			  AND state = 'active'`).Scan(&blocked); err != nil {
			t.Fatalf("inspect backend waits: %v", err)
		}
		if blocked > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no backend blocked on the profile row lock within the deadline")
}

// A credential reset that commits between verification and session insertion
// must not leave a session authenticated with the revoked credential.
func TestDirectProfileLoginLosesRaceWithConcurrentCredentialReset(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "login-race")
	if err := credentials.Set(ctx, accountID, profileID, "race@example.test", "first-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, _, sessions := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)

	// Hold the profile row exactly as a credential reset does, so the login's
	// session insert has to queue behind it.
	rotation, err := credentials.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rotation: %v", err)
	}
	defer func() { _ = rotation.Rollback(ctx) }()
	if _, err := rotation.Exec(ctx, `SELECT 1 FROM user_profiles WHERE user_id = $1 AND id = $2 FOR UPDATE`, accountID, profileID); err != nil {
		t.Fatalf("lock profile row: %v", err)
	}

	loginErr := make(chan error, 1)
	go func() {
		_, _, err := service.LoginProfile(ctx, "race@example.test", "first-password", DeviceClaim{ID: "race-device"})
		loginErr <- err
	}()
	waitForBlockedBackend(t, ctx, credentials.pool)

	rotated, err := bcrypt.GenerateFromPassword([]byte("second-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotation.Exec(ctx, `
		UPDATE user_profiles
		SET password_hash = $3, credential_revision = credential_revision + 1
		WHERE user_id = $1 AND id = $2`, accountID, profileID, string(rotated)); err != nil {
		t.Fatalf("rotate credential: %v", err)
	}
	if err := rotation.Commit(ctx); err != nil {
		t.Fatalf("commit rotation: %v", err)
	}

	if err := <-loginErr; !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("LoginProfile error = %v, want ErrSessionRevoked", err)
	}
	live, err := sessions.ListByUser(ctx, accountID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, session := range live {
		if session.AuthMethod == AuthMethodDirectProfile && session.RevokedAt == nil {
			t.Fatalf("session %s survived the credential reset it raced", session.ID)
		}
	}
}

// The same guard stated without concurrency: a subject verified at an older
// credential revision can never be turned into a session.
func TestCreateProfileSessionIfCurrentRejectsStaleSubject(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "stale-subject")
	if err := credentials.Set(ctx, accountID, profileID, "stale@example.test", "first-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	subject, err := credentials.Authenticate(ctx, "stale@example.test", "first-password", DeviceClaim{ID: "stale-device"})
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := credentials.Set(ctx, accountID, profileID, "stale@example.test", "second-password"); err != nil {
		t.Fatalf("rotate credential: %v", err)
	}

	sessions := NewSessionRepository(credentials.pool)
	err = sessions.CreateProfileSessionIfCurrent(ctx, directProfileSession("stale-session", subject), subject)
	if !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("CreateProfileSessionIfCurrent error = %v, want ErrSessionRevoked", err)
	}
	if _, err := sessions.GetByID(ctx, "stale-session"); !IsSessionNotFound(err) {
		t.Fatalf("stale session lookup error = %v, want session not found", err)
	}
}

func directProfileSession(id string, subject SessionSubject) models.AuthSession {
	profileID := subject.ProfileID
	revision := subject.CredentialRevision
	return models.AuthSession{
		ID:                        id,
		UserID:                    subject.AccountID,
		DeviceID:                  subject.Device.ID,
		ExpiresAt:                 time.Now().Add(time.Hour),
		ProfileID:                 &profileID,
		ProfileCredentialRevision: &revision,
		AuthMethod:                AuthMethodDirectProfile,
	}
}

// Refresh must revalidate against the persisted binding and current subject
// rather than trusting the facts inside the presented token.
func TestDirectProfileRefreshRejectsTokenClaimsThatOutrunTheSession(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "refresh-tamper")
	if err := credentials.Set(ctx, accountID, profileID, "tamper@example.test", "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, jwt, sessions := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)

	for name, tamper := range map[string]func(Claims, SessionSubject) Claims{
		"sibling profile": func(c Claims, _ SessionSubject) Claims { c.ProfileID = "profile-sibling"; return c },
		"other device":    func(c Claims, _ SessionSubject) Claims { c.DeviceID = "attacker-device"; return c },
		"older credential revision": func(c Claims, s SessionSubject) Claims {
			c.CredentialRevision = s.CredentialRevision - 1
			return c
		},
		"account auth method": func(c Claims, _ SessionSubject) Claims { c.AuthMethod = ""; return c },
	} {
		t.Run(name, func(t *testing.T) {
			// Each case needs its own live session: the first refused refresh
			// revokes it, and a revoked session would short-circuit the
			// binding check this test is about.
			pair, subject, err := service.LoginProfile(ctx, "tamper@example.test", "profile-password", DeviceClaim{ID: "tamper-device"})
			if err != nil {
				t.Fatalf("LoginProfile: %v", err)
			}
			live, err := jwt.ValidateToken(pair.RefreshToken)
			if err != nil {
				t.Fatalf("validate refresh token: %v", err)
			}

			forged, err := jwt.generateRefreshToken(tamper(*live, subject))
			if err != nil {
				t.Fatalf("mint tampered refresh token: %v", err)
			}
			if _, err := service.Refresh(ctx, forged); !errors.Is(err, ErrSessionRevoked) {
				t.Fatalf("Refresh error = %v, want ErrSessionRevoked", err)
			}
			session, err := sessions.GetByID(ctx, live.SessionID)
			if err != nil {
				t.Fatalf("load session: %v", err)
			}
			if session.RevokedAt == nil {
				t.Fatal("a refresh that failed its binding check left the session active")
			}
		})
	}
}

// An out-of-band credential change bumps the revision without going through
// the service's session sweep; refresh must still refuse the old binding.
func TestDirectProfileRefreshRefusesRotatedCredential(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "refresh-rotated")
	if err := credentials.Set(ctx, accountID, profileID, "rotated@example.test", "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, jwt, sessions := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)
	pair, _, err := service.LoginProfile(ctx, "rotated@example.test", "profile-password", DeviceClaim{ID: "rotated-device"})
	if err != nil {
		t.Fatalf("LoginProfile: %v", err)
	}
	claims, err := jwt.ValidateToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("validate refresh token: %v", err)
	}
	if _, err := credentials.pool.Exec(ctx, `
		UPDATE user_profiles SET credential_revision = credential_revision + 1
		WHERE user_id = $1 AND id = $2`, accountID, profileID); err != nil {
		t.Fatalf("rotate credential revision: %v", err)
	}

	if _, err := service.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("Refresh error = %v, want ErrSessionRevoked", err)
	}
	session, err := sessions.GetByID(ctx, claims.SessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.RevokedAt == nil {
		t.Fatal("refresh after credential rotation left the session active")
	}
}

// A healthy refresh re-reads authoritative tenancy state and slides the
// session window, exactly as an account session does.
func TestDirectProfileRefreshMintsCurrentSubjectAndExtendsSession(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "refresh-current")
	if err := credentials.Set(ctx, accountID, profileID, "current@example.test", "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, jwt, sessions := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)
	pair, subject, err := service.LoginProfile(ctx, "current@example.test", "profile-password", DeviceClaim{ID: "current-device"})
	if err != nil {
		t.Fatalf("LoginProfile: %v", err)
	}
	claims, err := jwt.ValidateToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("validate refresh token: %v", err)
	}
	if _, err := credentials.pool.Exec(ctx, `
		UPDATE organizations SET policy_revision = policy_revision + 5 WHERE id::text = $1`, subject.OrganizationID); err != nil {
		t.Fatalf("bump policy revision: %v", err)
	}
	shortened := time.Now().Add(time.Minute).UTC()
	if err := sessions.ExtendExpiresAt(ctx, claims.SessionID, shortened); err != nil {
		t.Fatalf("shorten session window: %v", err)
	}

	refreshed, err := service.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	access, err := jwt.ValidateToken(refreshed.AccessToken)
	if err != nil {
		t.Fatalf("validate refreshed access token: %v", err)
	}
	if access.PolicyRevision != subject.PolicyRevision+5 {
		t.Fatalf("policy revision = %d, want the current %d", access.PolicyRevision, subject.PolicyRevision+5)
	}
	if access.ProfileID != profileID || access.DeviceID != "current-device" || access.AuthMethod != AuthMethodDirectProfile {
		t.Fatalf("refreshed claims = %#v, want the persisted direct profile binding", access)
	}
	session, err := sessions.GetByID(ctx, claims.SessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if !session.ExpiresAt.After(shortened) {
		t.Fatalf("session expiry = %s, want it slid past %s", session.ExpiresAt, shortened)
	}
}
