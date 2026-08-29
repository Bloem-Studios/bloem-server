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
	var accountIncarnation string
	if err := credentials.pool.QueryRow(ctx, `SELECT account_incarnation_id::text FROM users WHERE id=$1`, accountID).Scan(&accountIncarnation); err != nil {
		t.Fatalf("load account incarnation: %v", err)
	}
	if claims.AccountIncarnationID != accountIncarnation {
		t.Fatalf("direct-profile token incarnation = %q, want %q", claims.AccountIncarnationID, accountIncarnation)
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

// Credential rotation is enforced by the database, not by the service, so a
// write that never goes through ProfileCredentialService still rotates the
// revision and ends the sessions that credential authenticated.
func TestRawCredentialWriteRotatesRevisionAndRevokesSessions(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "raw-rotation")
	if err := credentials.Set(ctx, accountID, profileID, "raw@example.test", "first-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, jwt, sessions := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)
	pair, subject, err := service.LoginProfile(ctx, "raw@example.test", "first-password", DeviceClaim{ID: "raw-device"})
	if err != nil {
		t.Fatalf("LoginProfile: %v", err)
	}
	claims, err := jwt.ValidateToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("validate refresh token: %v", err)
	}

	// A raw password change that does not touch credential_revision at all.
	rotated, err := bcrypt.GenerateFromPassword([]byte("second-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.pool.Exec(ctx, `
		UPDATE user_profiles SET password_hash = $3 WHERE user_id = $1 AND id = $2`,
		accountID, profileID, string(rotated)); err != nil {
		t.Fatalf("raw credential write: %v", err)
	}

	var revision int64
	if err := credentials.pool.QueryRow(ctx, `
		SELECT credential_revision FROM user_profiles WHERE user_id = $1 AND id = $2`,
		accountID, profileID).Scan(&revision); err != nil {
		t.Fatalf("reload revision: %v", err)
	}
	if revision != subject.CredentialRevision+1 {
		t.Fatalf("credential revision = %d, want %d", revision, subject.CredentialRevision+1)
	}
	session, err := sessions.GetByID(ctx, claims.SessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.RevokedAt == nil {
		t.Fatal("a raw credential write left the direct profile session active")
	}
	if _, err := service.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("Refresh after raw rotation = %v, want ErrSessionRevoked", err)
	}

	// A writer that supplies its own revision cannot hold it still either.
	if _, err := credentials.pool.Exec(ctx, `
		UPDATE user_profiles SET password_hash = $3, credential_revision = $4
		WHERE user_id = $1 AND id = $2`,
		accountID, profileID, string(rotated)+"x", revision); err != nil {
		t.Fatalf("raw credential write with pinned revision: %v", err)
	}
	var pinned int64
	if err := credentials.pool.QueryRow(ctx, `
		SELECT credential_revision FROM user_profiles WHERE user_id = $1 AND id = $2`,
		accountID, profileID).Scan(&pinned); err != nil {
		t.Fatalf("reload pinned revision: %v", err)
	}
	if pinned != revision+1 {
		t.Fatalf("pinned revision = %d, want the forced %d", pinned, revision+1)
	}
}

// The stored credential pair must be a real pair: the database refuses a blank
// email or hash rather than registering an empty login key.
func TestProfileCredentialPairRejectsBlankValues(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "blank-pair")

	// The blank cases cover every whitespace Go's strings.TrimSpace removes:
	// btrim's default set is spaces alone, so a tab or a non-breaking space
	// would otherwise pass for a real credential.
	for name, statement := range map[string]string{
		"blank email":   `UPDATE user_profiles SET login_email = '', password_hash = 'hash' WHERE user_id = $1 AND id = $2`,
		"space email":   `UPDATE user_profiles SET login_email = '   ', password_hash = 'hash' WHERE user_id = $1 AND id = $2`,
		"tab email":     `UPDATE user_profiles SET login_email = E'\t', password_hash = 'hash' WHERE user_id = $1 AND id = $2`,
		"newline email": `UPDATE user_profiles SET login_email = E'\r\n', password_hash = 'hash' WHERE user_id = $1 AND id = $2`,
		"unicode email": `UPDATE user_profiles SET login_email = U&'\00a0\3000', password_hash = 'hash' WHERE user_id = $1 AND id = $2`,
		"blank hash":    `UPDATE user_profiles SET login_email = 'blank@example.test', password_hash = '' WHERE user_id = $1 AND id = $2`,
		"tab hash":      `UPDATE user_profiles SET login_email = 'blank@example.test', password_hash = E'\t' WHERE user_id = $1 AND id = $2`,
		"unicode hash":  `UPDATE user_profiles SET login_email = 'blank@example.test', password_hash = U&'\00a0' WHERE user_id = $1 AND id = $2`,
		"email only":    `UPDATE user_profiles SET login_email = 'blank@example.test' WHERE user_id = $1 AND id = $2`,
		"hash only":     `UPDATE user_profiles SET password_hash = 'hash' WHERE user_id = $1 AND id = $2`,
	} {
		t.Run(name, func(t *testing.T) {
			// Each case starts from a shared-only profile: these subtests run
			// in map order against one row, so a write that slipped through
			// would otherwise change what the next case is even asking.
			if _, err := credentials.pool.Exec(ctx, `
				UPDATE user_profiles SET login_email = NULL, password_hash = NULL
				WHERE user_id = $1 AND id = $2`, accountID, profileID); err != nil {
				t.Fatalf("reset credential pair: %v", err)
			}
			if _, err := credentials.pool.Exec(ctx, statement, accountID, profileID); err == nil {
				t.Fatal("database accepted an incomplete credential pair")
			}
		})
	}
}

type failingSubjectResolver struct {
	err   error
	inner directProfileCredentials
}

func (r failingSubjectResolver) Authenticate(
	ctx context.Context, email, password string, device DeviceClaim,
) (SessionSubject, error) {
	return r.inner.Authenticate(ctx, email, password, device)
}

func (r failingSubjectResolver) CurrentSessionSubject(
	context.Context, int, string, int64, DeviceClaim,
) (SessionSubject, error) {
	return SessionSubject{}, r.err
}

// A database that is briefly unreachable says nothing about whether a binding
// is still valid, so refresh must fail without destroying the session.
func TestDirectProfileRefreshKeepsSessionWhenRevalidationFails(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "refresh-transient")
	if err := credentials.Set(ctx, accountID, profileID, "transient@example.test", "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, jwt, sessions := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)
	pair, _, err := service.LoginProfile(ctx, "transient@example.test", "profile-password", DeviceClaim{ID: "transient-device"})
	if err != nil {
		t.Fatalf("LoginProfile: %v", err)
	}
	claims, err := jwt.ValidateToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("validate refresh token: %v", err)
	}

	transient := errors.New("connection reset by peer")
	service.profileCredentials = failingSubjectResolver{err: transient, inner: credentials.ProfileCredentialService}

	_, err = service.Refresh(ctx, pair.RefreshToken)
	if err == nil || errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("Refresh error = %v, want an operational failure rather than revocation", err)
	}
	if !errors.Is(err, transient) {
		t.Fatalf("Refresh error = %v, want it to wrap the underlying failure", err)
	}
	session, err := sessions.GetByID(ctx, claims.SessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.RevokedAt != nil {
		t.Fatal("a transient revalidation failure revoked a valid session")
	}
}

// One canonical normalization: the database owns it. Go trimming and lowering
// separately would mean two implementations of "the same email", and they do
// not agree on Unicode case for every input, so an address that registered
// could fail to authenticate.
func TestDirectProfileLoginNormalizesEmailInTheDatabase(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "normalization")
	const stored = "  MiXeD.Case@Example.TEST  "
	if err := credentials.Set(ctx, accountID, profileID, stored, "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}

	var key string
	if err := credentials.pool.QueryRow(ctx, `
		SELECT normalized_email FROM login_email_registry
		WHERE profile_user_id = $1 AND profile_id = $2`, accountID, profileID).Scan(&key); err != nil {
		t.Fatalf("load registry key: %v", err)
	}
	if key != "mixed.case@example.test" {
		t.Fatalf("registry key = %q, want the database-normalized form", key)
	}

	for _, attempt := range []string{
		"mixed.case@example.test",
		"MIXED.CASE@EXAMPLE.TEST",
		"  MiXeD.Case@Example.TEST\t",
	} {
		if _, err := credentials.Authenticate(ctx, attempt, "profile-password", DeviceClaim{ID: "normalization-device"}); err != nil {
			t.Fatalf("Authenticate(%q) = %v, want the same subject", attempt, err)
		}
	}
}

// Re-saving a record without changing its login identity must be a no-op for
// the registry. User management legitimately writes the current email back,
// and a registry that re-registers on every write turns an ordinary admin save
// into a duplicate-key failure.
func TestLoginEmailRegistryToleratesUnchangedWrites(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	users := NewUserRepository(service.pool)
	account, err := users.Create(ctx, models.CreateUserInput{
		Username: "unchanged-writes",
		Email:    "unchanged-writes@example.test",
		Password: "account-password",
		Role:     "user",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	for name, statement := range map[string]string{
		"same email":      `UPDATE users SET email = 'unchanged-writes@example.test' WHERE id = $1`,
		"self assigned":   `UPDATE users SET email = email WHERE id = $1`,
		"unrelated field": `UPDATE users SET email = email, role = 'user' WHERE id = $1`,
	} {
		t.Run("account "+name, func(t *testing.T) {
			if _, err := service.pool.Exec(ctx, statement, account.ID); err != nil {
				t.Fatalf("no-op account update: %v", err)
			}
		})
	}
	var owned int
	if err := service.pool.QueryRow(ctx, `
		SELECT count(*) FROM login_email_registry WHERE account_id = $1`, account.ID).Scan(&owned); err != nil {
		t.Fatalf("count registry rows: %v", err)
	}
	if owned != 1 {
		t.Fatalf("account registry rows = %d, want exactly 1", owned)
	}

	accountID, profileID := newProfileCredentialFixture(t, service.pool, "unchanged-profile")
	if err := service.Set(ctx, accountID, profileID, "unchanged-profile@example.test", "profile-password"); err != nil {
		t.Fatalf("set profile credential: %v", err)
	}
	if _, err := service.pool.Exec(ctx, `
		UPDATE user_profiles SET login_email = login_email, password_hash = password_hash
		WHERE user_id = $1 AND id = $2`, accountID, profileID); err != nil {
		t.Fatalf("no-op profile credential update: %v", err)
	}
	// A no-op must also leave the credential revision alone: rotating it would
	// revoke every live session for a write that changed nothing.
	var revision int64
	var registered string
	if err := service.pool.QueryRow(ctx, `
		SELECT p.credential_revision, r.normalized_email
		FROM user_profiles p
		JOIN login_email_registry r ON r.profile_user_id = p.user_id AND r.profile_id = p.id
		WHERE p.user_id = $1 AND p.id = $2`, accountID, profileID).Scan(&revision, &registered); err != nil {
		t.Fatalf("reload profile credential: %v", err)
	}
	if registered != "unchanged-profile@example.test" {
		t.Fatalf("registered email = %q, want it preserved", registered)
	}
	if revision != 2 {
		t.Fatalf("credential revision = %d, want the single rotation from Set", revision)
	}
}

// The subject a direct-profile session asserts is not only its credential: it
// also claims the account is enabled and the organization and membership are
// active at the revisions it carries. Each of those must be held still between
// verification and insertion, or the session is issued against facts that
// changed underneath it.
func TestDirectProfileLoginLosesRaceWithSubjectChanges(t *testing.T) {
	for name, mutation := range map[string]string{
		"account disabled":       `UPDATE users SET enabled = false WHERE id = $1`,
		"organization suspended": `UPDATE organizations SET status = 'suspended' WHERE id = (SELECT organization_id FROM user_profiles WHERE user_id = $1 LIMIT 1)`,
		"policy rotated":         `UPDATE organizations SET policy_revision = policy_revision + 1 WHERE id = (SELECT organization_id FROM user_profiles WHERE user_id = $1 LIMIT 1)`,
		"membership suspended":   `UPDATE organization_memberships SET status = 'suspended' WHERE account_id = $1`,
		"security rotated":       `UPDATE organization_memberships SET security_revision = security_revision + 1 WHERE account_id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			credentials := newProfileCredentialService(t)
			accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "subject-race")
			const email = "subject-race@example.test"
			if err := credentials.Set(ctx, accountID, profileID, email, "profile-password"); err != nil {
				t.Fatalf("Set credential: %v", err)
			}
			service, _, sessions := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)

			// Apply the change without committing. The row it touches is one
			// the login must read under a share lock, so the login parks
			// behind it; committing then releases a login that has to observe
			// the new value.
			//
			// The writer deliberately holds only the row it is changing. A
			// blocker that grabbed the profile row first would be taking locks
			// in the reverse of the order this repository uses, and would
			// deadlock rather than test anything.
			blocker, err := credentials.pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin blocker: %v", err)
			}
			defer func() { _ = blocker.Rollback(ctx) }()
			if _, err := blocker.Exec(ctx, mutation, accountID); err != nil {
				t.Fatalf("apply %s: %v", name, err)
			}

			loginErr := make(chan error, 1)
			go func() {
				_, _, err := service.LoginProfile(ctx, email, "profile-password", DeviceClaim{ID: "subject-race-device"})
				loginErr <- err
			}()
			waitForBlockedBackend(t, ctx, credentials.pool)

			if err := blocker.Commit(ctx); err != nil {
				t.Fatalf("commit %s: %v", name, err)
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
					t.Fatalf("session %s was issued against a subject that had already changed", session.ID)
				}
			}
		})
	}
}

// Session creation and profile-group administration touch the same membership
// and profile rows. Administration locks the membership first, so session
// creation must too: taking them in the opposite order lets the two form a
// cycle and aborts one of them with a deadlock instead of doing its work.
func TestDirectProfileLoginDoesNotDeadlockWithMembershipFirstWriters(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "deadlock-order")
	const email = "deadlock-order@example.test"
	if err := credentials.Set(ctx, accountID, profileID, email, "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, _, _ := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)

	// An administrative writer in the established order: membership, then the
	// profile it is moving.
	writer, err := credentials.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	defer func() { _ = writer.Rollback(ctx) }()
	if _, err := writer.Exec(ctx, `
		SELECT 1 FROM organization_memberships WHERE account_id = $1 FOR UPDATE`, accountID); err != nil {
		t.Fatalf("lock membership: %v", err)
	}

	loginErr := make(chan error, 1)
	go func() {
		_, _, err := service.LoginProfile(ctx, email, "profile-password", DeviceClaim{ID: "deadlock-device"})
		loginErr <- err
	}()
	waitForBlockedBackend(t, ctx, credentials.pool)

	// The writer now takes the profile row, which the login must not already
	// be holding. If session creation locked the profile first, this is where
	// the cycle would close.
	if _, err := writer.Exec(ctx, `
		SELECT 1 FROM user_profiles WHERE user_id = $1 AND id = $2 FOR UPDATE`, accountID, profileID); err != nil {
		t.Fatalf("writer could not take the profile row in order: %v", err)
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatalf("commit writer: %v", err)
	}

	if err := <-loginErr; err != nil {
		t.Fatalf("LoginProfile = %v, want it to succeed once the writer committed", err)
	}
}

// The device binding is enforced on every request after login, so it must be
// impossible to mint a direct session without one — at the service, for any
// adapter that skips the HTTP handler, and in the database, for any writer
// that skips the service.
func TestDirectProfileSessionRequiresADevice(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "device-required")
	if err := credentials.Set(ctx, accountID, profileID, "device-required@example.test", "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, _, _ := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)

	for name, deviceID := range map[string]string{
		"empty":      "",
		"whitespace": " \t ",
	} {
		t.Run("service refuses a "+name+" device", func(t *testing.T) {
			_, _, err := service.LoginProfile(ctx, "device-required@example.test", "profile-password", DeviceClaim{ID: deviceID})
			if !errors.Is(err, ErrDeviceRequired) {
				t.Fatalf("LoginProfile = %v, want ErrDeviceRequired", err)
			}
		})
	}

	t.Run("database refuses a blank device on a direct session", func(t *testing.T) {
		var revision int64
		if err := credentials.pool.QueryRow(ctx, `
			SELECT credential_revision FROM user_profiles WHERE user_id = $1 AND id = $2`,
			accountID, profileID).Scan(&revision); err != nil {
			t.Fatalf("load revision: %v", err)
		}
		if _, err := credentials.pool.Exec(ctx, `
			INSERT INTO auth_sessions (id, user_id, device_name, device_id, expires_at, profile_id, profile_credential_revision, auth_method)
			VALUES ('blank-device-session', $1, 'Blank', '', now() + interval '1 hour', $2, $3, 'direct_profile')`,
			accountID, profileID, revision); err == nil {
			t.Fatal("database accepted a direct profile session with no device")
		}
	})
}

// Every failed authentication must cost exactly one bcrypt comparison, or
// response timing separates registered direct-profile emails from unknown
// ones regardless of the uniform error body.
func TestAuthenticateCostsOneComparisonOnEveryFailurePath(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "timing")
	if err := credentials.Set(ctx, accountID, profileID, "timing@example.test", "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}

	comparisons := 0
	original := bcryptCompare
	bcryptCompare = func(hash, password []byte) error {
		comparisons++
		return original(hash, password)
	}
	defer func() { bcryptCompare = original }()

	for name, email := range map[string]string{
		"registered email, wrong password": "timing@example.test",
		"unknown email":                    "nobody@example.test",
	} {
		t.Run(name, func(t *testing.T) {
			comparisons = 0
			_, err := credentials.Authenticate(ctx, email, "wrong-password", DeviceClaim{ID: "timing-device"})
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Authenticate = %v, want ErrInvalidCredentials", err)
			}
			if comparisons != 1 {
				t.Fatalf("bcrypt comparisons = %d, want exactly 1", comparisons)
			}
		})
	}

	// A malformed stored hash must not shortcut either.
	t.Run("malformed stored hash", func(t *testing.T) {
		if _, err := credentials.pool.Exec(ctx, `
			UPDATE user_profiles SET password_hash = 'not-a-bcrypt-hash' WHERE user_id = $1 AND id = $2`,
			accountID, profileID); err != nil {
			t.Fatalf("corrupt stored hash: %v", err)
		}
		comparisons = 0
		_, err := credentials.Authenticate(ctx, "timing@example.test", "wrong-password", DeviceClaim{ID: "timing-device"})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Authenticate = %v, want ErrInvalidCredentials", err)
		}
		if comparisons != 1 {
			t.Fatalf("bcrypt comparisons = %d, want exactly 1", comparisons)
		}
	})
}
