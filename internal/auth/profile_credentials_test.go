package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestProfileCredentialsRejectPartialPair(t *testing.T) {
	service := newProfileCredentialService(t)
	_, profileID := newProfileCredentialFixture(t, service.pool, "partial")

	err := service.Set(context.Background(), 1, profileID, "reader@example.test", "")
	if !errors.Is(err, ErrIncompleteProfileCredentials) {
		t.Fatalf("Set partial profile credentials error = %v, want ErrIncompleteProfileCredentials", err)
	}
}

func TestProfileCredentialsSetClearAndAuthenticate(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, service.pool, "complete")

	if err := service.Set(ctx, accountID, profileID, "Reader@Example.Test", "profile-password"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var storedEmail, storedHash string
	var revision int64
	if err := service.pool.QueryRow(ctx, `
		SELECT login_email, password_hash, credential_revision
		FROM user_profiles
		WHERE user_id = $1 AND id = $2`, accountID, profileID).Scan(&storedEmail, &storedHash, &revision); err != nil {
		t.Fatalf("load stored profile credential: %v", err)
	}
	if storedEmail != "Reader@Example.Test" {
		t.Fatalf("stored email = %q, want original display value", storedEmail)
	}
	if storedHash == "profile-password" || bcrypt.CompareHashAndPassword([]byte(storedHash), []byte("profile-password")) != nil {
		t.Fatal("profile password was not stored as a bcrypt hash")
	}
	if revision <= 1 {
		t.Fatalf("credential revision = %d, want incremented revision", revision)
	}

	subject, err := service.Authenticate(ctx, "reader@example.test", "profile-password", DeviceClaim{ID: "device-complete"})
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if subject.AccountID != accountID || subject.ProfileID != profileID || subject.Device.ID != "device-complete" || subject.AuthMethod != AuthMethodDirectProfile {
		t.Fatalf("subject = %#v, want profile-bound direct-login subject", subject)
	}
	if subject.CredentialRevision != revision || subject.OrganizationID == "" || subject.MembershipID == "" || subject.PolicyRevision <= 0 || subject.SecurityRevision <= 0 {
		t.Fatalf("subject = %#v, want current tenant and credential revisions", subject)
	}

	if err := service.Set(ctx, accountID, profileID, "", ""); err != nil {
		t.Fatalf("Set empty pair: %v", err)
	}
	if _, err := service.Authenticate(ctx, "reader@example.test", "profile-password", DeviceClaim{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate after Clear error = %v, want ErrInvalidCredentials", err)
	}
}

func TestProfileCredentialsRejectCaseInsensitiveAccountAndProfileCollisions(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, service.pool, "first")
	otherAccountID, otherProfileID := newProfileCredentialFixture(t, service.pool, "second")

	if err := service.Set(ctx, accountID, profileID, "owned@example.test", "profile-password"); err != nil {
		t.Fatalf("Set first profile: %v", err)
	}
	if err := service.Set(ctx, otherAccountID, otherProfileID, "OWNED@example.test", "profile-password"); !errors.Is(err, ErrCredentialEmailInUse) {
		t.Fatalf("Set colliding profile error = %v, want ErrCredentialEmailInUse", err)
	}

	if err := service.Set(ctx, otherAccountID, otherProfileID, "fixture-first@example.test", "profile-password"); !errors.Is(err, ErrCredentialEmailInUse) {
		t.Fatalf("Set colliding account email error = %v, want ErrCredentialEmailInUse", err)
	}
}

func TestDirectProfileLoginRejectsDisabledOrSuspendedSubject(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, service.pool, "disabled")
	if err := service.Set(ctx, accountID, profileID, "disabled-profile@example.test", "profile-password"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, err := service.pool.Exec(ctx, `UPDATE users SET enabled = false WHERE id = $1`, accountID); err != nil {
		t.Fatalf("disable account: %v", err)
	}
	if _, err := service.Authenticate(ctx, "disabled-profile@example.test", "profile-password", DeviceClaim{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate disabled account error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE users SET enabled = true WHERE id = $1`, accountID); err != nil {
		t.Fatalf("enable account: %v", err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE organization_memberships SET status = 'suspended' WHERE account_id = $1`, accountID); err != nil {
		t.Fatalf("suspend membership: %v", err)
	}
	if _, err := service.Authenticate(ctx, "disabled-profile@example.test", "profile-password", DeviceClaim{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate suspended membership error = %v, want ErrInvalidCredentials", err)
	}
}

func TestProfileCredentialPasswordResetInvalidatesPriorPassword(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, service.pool, "reset")
	if err := service.Set(ctx, accountID, profileID, "reset-profile@example.test", "first-password"); err != nil {
		t.Fatalf("Set first credential: %v", err)
	}
	before, err := service.Authenticate(ctx, "reset-profile@example.test", "first-password", DeviceClaim{})
	if err != nil {
		t.Fatalf("Authenticate first password: %v", err)
	}
	if err := service.Set(ctx, accountID, profileID, "reset-profile@example.test", "second-password"); err != nil {
		t.Fatalf("reset credential: %v", err)
	}
	if _, err := service.Authenticate(ctx, "reset-profile@example.test", "first-password", DeviceClaim{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate old password error = %v, want ErrInvalidCredentials", err)
	}
	after, err := service.Authenticate(ctx, "reset-profile@example.test", "second-password", DeviceClaim{})
	if err != nil {
		t.Fatalf("Authenticate new password: %v", err)
	}
	if after.CredentialRevision <= before.CredentialRevision {
		t.Fatalf("credential revision = %d after reset, want greater than %d", after.CredentialRevision, before.CredentialRevision)
	}
}

func TestDirectProfileLoginPasswordResetRevokesProfileSession(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "session-revocation")
	if err := credentials.Set(ctx, accountID, profileID, "session-profile@example.test", "first-password"); err != nil {
		t.Fatalf("Set first credential: %v", err)
	}
	sessions := NewSessionRepository(credentials.pool)
	jwt := NewJWTService("profile-session-secret", time.Hour, 24*time.Hour)
	service := NewService(nil, jwt, sessions, NewUserRepository(credentials.pool), NewInviteCodeRepository(credentials.pool), nil, nil)
	service.SetProfileCredentialService(credentials.ProfileCredentialService)

	pair, subject, err := service.LoginProfile(ctx, "session-profile@example.test", "first-password", DeviceClaim{ID: "device-session", Name: "Reader"})
	if err != nil {
		t.Fatalf("LoginProfile: %v", err)
	}
	claims, err := jwt.ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("validate access token: %v", err)
	}
	if claims.ProfileID != profileID || claims.DeviceID != "device-session" || claims.AuthMethod != AuthMethodDirectProfile || claims.CredentialRevision != subject.CredentialRevision || claims.OrganizationID != subject.OrganizationID || claims.MembershipID != subject.MembershipID {
		t.Fatalf("claims = %#v, want profile-bound direct session", claims)
	}
	session, err := sessions.GetByID(ctx, claims.SessionID)
	if err != nil {
		t.Fatalf("load direct profile session: %v", err)
	}
	if session.DeviceID != "device-session" || session.ProfileID == nil || *session.ProfileID != profileID || session.ProfileCredentialRevision == nil || *session.ProfileCredentialRevision != subject.CredentialRevision || session.AuthMethod != AuthMethodDirectProfile {
		t.Fatalf("session = %#v, want persisted direct profile binding", session)
	}
	if err := credentials.Set(ctx, accountID, profileID, "session-profile@example.test", "second-password"); err != nil {
		t.Fatalf("reset credential: %v", err)
	}
	session, err = sessions.GetByID(ctx, claims.SessionID)
	if err != nil {
		t.Fatalf("load revoked profile session: %v", err)
	}
	if session.RevokedAt == nil {
		t.Fatal("password reset left the direct profile session active")
	}
}

func TestProfileCredentialsConcurrentDuplicateEmailWritesAllowOneOwner(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	accountA, profileA := newProfileCredentialFixture(t, service.pool, "concurrent-a")
	accountB, profileB := newProfileCredentialFixture(t, service.pool, "concurrent-b")

	start := make(chan struct{})
	errorsByAttempt := make(chan error, 2)
	var wg sync.WaitGroup
	for _, credential := range []struct {
		accountID int
		profileID string
	}{{accountA, profileA}, {accountB, profileB}} {
		wg.Add(1)
		go func(accountID int, profileID string) {
			defer wg.Done()
			<-start
			errorsByAttempt <- service.Set(ctx, accountID, profileID, "shared@example.test", "profile-password")
		}(credential.accountID, credential.profileID)
	}
	close(start)
	wg.Wait()
	close(errorsByAttempt)

	var successes, collisions int
	for err := range errorsByAttempt {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrCredentialEmailInUse):
			collisions++
		default:
			t.Fatalf("concurrent Set error = %v", err)
		}
	}
	if successes != 1 || collisions != 1 {
		t.Fatalf("concurrent result successes=%d collisions=%d, want 1 each", successes, collisions)
	}
}

func TestProfileCredentialWriteErrorDoesNotExposeEmail(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, service.pool, "redaction-a")
	otherID, otherProfileID := newProfileCredentialFixture(t, service.pool, "redaction-b")
	const email = "private-login@example.test"
	if err := service.Set(ctx, accountID, profileID, email, "profile-password"); err != nil {
		t.Fatal(err)
	}
	err := service.Set(ctx, otherID, otherProfileID, email, "profile-password")
	if !errors.Is(err, ErrCredentialEmailInUse) {
		t.Fatalf("Set error = %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "private-login@example.test") {
		t.Fatalf("error leaks login email: %v", err)
	}
}

func TestProfileCredentialRegistryTracksDirectProfileSQLWrites(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, service.pool, "sql-a")
	otherAccountID, otherProfileID := newProfileCredentialFixture(t, service.pool, "sql-b")
	hash, err := bcrypt.GenerateFromPassword([]byte("profile-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE user_profiles SET login_email = 'SQL@Example.test', password_hash = $3 WHERE user_id = $1 AND id = $2`, accountID, profileID, string(hash)); err != nil {
		t.Fatalf("direct credential write: %v", err)
	}
	var owner string
	if err := service.pool.QueryRow(ctx, `SELECT profile_id FROM login_email_registry WHERE normalized_email = 'sql@example.test'`).Scan(&owner); err != nil || owner != profileID {
		t.Fatalf("registry owner = %q, err = %v", owner, err)
	}
	// The colliding write must target a row that exists, or "no rows updated"
	// would masquerade as an enforced collision.
	collision, err := service.pool.Exec(ctx, `UPDATE user_profiles SET login_email = 'sql@example.test', password_hash = $3 WHERE user_id = $1 AND id = $2`, otherAccountID, otherProfileID, string(hash))
	if err == nil {
		t.Fatalf("direct SQL collision succeeded, rows affected = %d", collision.RowsAffected())
	}
	if _, err := service.pool.Exec(ctx, `UPDATE user_profiles SET login_email = NULL, password_hash = NULL WHERE user_id = $1 AND id = $2`, accountID, profileID); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM login_email_registry WHERE normalized_email = 'sql@example.test')`).Scan(&exists); err != nil || exists {
		t.Fatalf("registry clear exists=%v err=%v", exists, err)
	}
}

type profileCredentialTestService struct {
	*ProfileCredentialService
	pool *pgxpool.Pool
}

func newProfileCredentialService(t *testing.T) profileCredentialTestService {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newAuthTenancyDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	activateProfileCredentialTestTenant(t, pool)
	return profileCredentialTestService{ProfileCredentialService: NewProfileCredentialService(pool), pool: pool}
}

func activateProfileCredentialTestTenant(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	users := NewUserRepository(pool)
	owner, err := users.Create(ctx, models.CreateUserInput{
		Username: "credential-owner",
		Email:    "credential-owner@example.test",
		Password: "owner-password",
		Role:     "admin",
	})
	if err != nil {
		t.Fatalf("create fixture owner: %v", err)
	}
	tenants := tenancy.NewStore(pool)
	if _, err := tenants.ProvisionDefaultMembership(ctx, owner.ID, "admin"); err != nil {
		t.Fatalf("provision fixture owner membership: %v", err)
	}
	if _, err := tenants.ActivateInitialOwnership(ctx, owner.ID); err != nil {
		t.Fatalf("activate fixture owner: %v", err)
	}
}

func newProfileCredentialFixture(t *testing.T, pool *pgxpool.Pool, suffix string) (int, string) {
	t.Helper()
	ctx := context.Background()
	users := NewUserRepository(pool)
	user, err := users.Create(ctx, models.CreateUserInput{
		Username: suffix + "-user",
		Email:    "fixture-" + suffix + "@example.test",
		Password: "account-password",
		Role:     "user",
	})
	if err != nil {
		t.Fatalf("create fixture account: %v", err)
	}
	tenants := tenancy.NewStore(pool)
	if _, err := tenants.ProvisionDefaultMembership(ctx, user.ID, "user"); err != nil {
		t.Fatalf("provision fixture membership: %v", err)
	}
	organization, err := tenants.DefaultOrganization(ctx)
	if err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	var groupID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id = $1 AND is_default`, organization.ID).Scan(&groupID); err != nil {
		t.Fatalf("load default access group: %v", err)
	}
	profileID := "profile-" + suffix
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (id, user_id, name, organization_id, access_group_id)
		VALUES ($1, $2, $3, $4, $5)`, profileID, user.ID, suffix, organization.ID, groupID); err != nil {
		t.Fatalf("create fixture profile: %v", err)
	}
	return user.ID, profileID
}
