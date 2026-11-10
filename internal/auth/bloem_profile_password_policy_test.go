package auth

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth/passwordhash"
	"github.com/Silo-Server/silo-server/internal/config"
)

// setPasswordSignIn writes the server-wide local password switch straight to
// server_settings, which is what every policy decision reads.
func setPasswordSignIn(t *testing.T, pool *pgxpool.Pool, on bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO server_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		config.AuthLocalPasswordLoginSettingKey, strconv.FormatBool(on)); err != nil {
		t.Fatalf("set %s: %v", config.AuthLocalPasswordLoginSettingKey, err)
	}
}

// setAccountPasswordSignIn turns one account's own password sign-in on or
// off, as linking an OIDC or LDAP identity does.
func setAccountPasswordSignIn(t *testing.T, pool *pgxpool.Pool, accountID int, on bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE users SET local_password_login_enabled = $2 WHERE id = $1`, accountID, on); err != nil {
		t.Fatalf("set account password sign-in: %v", err)
	}
}

// refuseByNetworkProvider records an enabled network provider's refusal of
// the account's person, the state networkRefused reports.
func refuseByNetworkProvider(t *testing.T, pool *pgxpool.Pool, accountID int) {
	t.Helper()
	ctx := context.Background()
	var installation int
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_installations (plugin_id, version, install_path, enabled, update_policy, kind)
		VALUES ('profile-policy-network', '0', '/nonexistent/profile-policy-network', true, 'manual', 'plugin')
		RETURNING id`).Scan(&installation); err != nil {
		t.Fatalf("create network provider: %v", err)
	}
	for _, statement := range []string{
		`INSERT INTO plugin_capabilities (plugin_installation_id, capability_type, capability_id, metadata)
			VALUES ($1, 'auth_provider.v1', 'peer-network', '{"auth_modes":["network"]}')`,
		`INSERT INTO plugin_auth_bindings (plugin_installation_id, capability_id, enabled) VALUES ($1, 'peer-network', true)`,
	} {
		if _, err := pool.Exec(ctx, statement, installation); err != nil {
			t.Fatalf("configure network provider: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plugin_auth_identities (
			plugin_installation_id, external_subject, user_id, issuer, username, email, display_name,
			linked_at, last_checked_at, last_check_status
		) VALUES ($1, 'profile-policy-person', $2, '', '', '', '', NOW(), NOW(), $3)`,
		installation, accountID, CheckStatusNotFound); err != nil {
		t.Fatalf("record network refusal: %v", err)
	}
}

// Turning password sign-in off for the server refuses a correct profile
// password, as it refuses an account password, while a wrong password still
// reads as a wrong password. Sessions already open keep refreshing, as
// account sessions do.
func TestDirectProfileLoginRefusedWhilePasswordSignInIsOff(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "policy-switch")
	const email = "policy-switch@example.test"
	if err := credentials.Set(ctx, accountID, profileID, email, "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, _, _ := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)
	open, _, err := service.LoginProfile(ctx, email, "profile-password", DeviceClaim{ID: "switch-device"})
	if err != nil {
		t.Fatalf("LoginProfile with password sign-in on: %v", err)
	}

	setPasswordSignIn(t, credentials.pool, false)
	if _, err := credentials.Authenticate(ctx, email, "profile-password", DeviceClaim{ID: "switch-device"}); !errors.Is(err, ErrLocalLoginDisabled) {
		t.Fatalf("Authenticate correct password = %v, want ErrLocalLoginDisabled", err)
	}
	if _, err := credentials.Authenticate(ctx, email, "wrong-password", DeviceClaim{ID: "switch-device"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate wrong password = %v, want ErrInvalidCredentials", err)
	}
	if _, _, err := service.LoginProfile(ctx, email, "profile-password", DeviceClaim{ID: "switch-device"}); !errors.Is(err, ErrLocalLoginDisabled) {
		t.Fatalf("LoginProfile = %v, want ErrLocalLoginDisabled", err)
	}
	if _, err := service.Refresh(ctx, open.RefreshToken); err != nil {
		t.Fatalf("Refresh of a session opened before the switch = %v, want a new pair", err)
	}
}

// The session insert repeats the policy checks under its locks, so a change
// committed between the password check and the insert still refuses it.
func TestDirectProfileSessionInsertRechecksPasswordPolicy(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "policy-insert")
	const email = "policy-insert@example.test"
	if err := credentials.Set(ctx, accountID, profileID, email, "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	sessions := NewSessionRepository(credentials.pool)
	authenticate := func() SessionSubject {
		t.Helper()
		subject, err := credentials.Authenticate(ctx, email, "profile-password", DeviceClaim{ID: "insert-device"})
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		return subject
	}

	subject := authenticate()
	setPasswordSignIn(t, credentials.pool, false)
	err := sessions.CreateProfileSessionIfCurrent(ctx, directProfileSession("switched-off-session", subject), subject)
	if !errors.Is(err, ErrLocalLoginDisabled) {
		t.Fatalf("insert after the server switch went off = %v, want ErrLocalLoginDisabled", err)
	}
	if _, err := sessions.GetByID(ctx, "switched-off-session"); !IsSessionNotFound(err) {
		t.Fatalf("refused session lookup = %v, want session not found", err)
	}

	setPasswordSignIn(t, credentials.pool, true)
	subject = authenticate()
	setAccountPasswordSignIn(t, credentials.pool, accountID, false)
	err = sessions.CreateProfileSessionIfCurrent(ctx, directProfileSession("account-off-session", subject), subject)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("insert after the account's password sign-in went off = %v, want ErrInvalidCredentials", err)
	}
	if _, err := sessions.GetByID(ctx, "account-off-session"); !IsSessionNotFound(err) {
		t.Fatalf("refused session lookup = %v, want session not found", err)
	}
}

// An account whose own password sign-in is off refuses its profiles'
// passwords exactly like a wrong password, at the same cost, and its open
// direct-profile sessions end at their next refresh.
func TestDirectProfileLoginEndsWhenAccountPasswordSignInIsOff(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "policy-account")
	const email = "policy-account@example.test"
	if err := credentials.Set(ctx, accountID, profileID, email, "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, jwt, sessions := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)
	open, _, err := service.LoginProfile(ctx, email, "profile-password", DeviceClaim{ID: "account-device"})
	if err != nil {
		t.Fatalf("LoginProfile: %v", err)
	}

	setAccountPasswordSignIn(t, credentials.pool, accountID, false)
	comparisons := 0
	t.Cleanup(func() { comparePasswordHash = passwordhash.Compare })
	comparePasswordHash = func(ctx context.Context, hash, password []byte) error {
		comparisons++
		return passwordhash.Compare(ctx, hash, password)
	}
	if _, err := credentials.Authenticate(ctx, email, "profile-password", DeviceClaim{ID: "account-device"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate = %v, want ErrInvalidCredentials", err)
	}
	if comparisons != 1 {
		t.Fatalf("bcrypt comparisons = %d, want exactly 1", comparisons)
	}

	if _, err := service.Refresh(ctx, open.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("Refresh = %v, want ErrSessionRevoked", err)
	}
	claims, err := jwt.ValidateToken(open.AccessToken)
	if err != nil {
		t.Fatalf("validate access token: %v", err)
	}
	session, err := sessions.GetByID(ctx, claims.SessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.RevokedAt == nil {
		t.Fatal("refresh refused the session but left it active")
	}
}

// A network provider's refusal of the account's person refuses its profiles'
// passwords too, with no break-glass exemption, and ends open sessions at
// refresh.
func TestDirectProfileLoginRefusedByNetworkProvider(t *testing.T) {
	ctx := context.Background()
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "policy-network")
	const email = "policy-network@example.test"
	if err := credentials.Set(ctx, accountID, profileID, email, "profile-password"); err != nil {
		t.Fatalf("Set credential: %v", err)
	}
	service, _, _ := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)
	open, _, err := service.LoginProfile(ctx, email, "profile-password", DeviceClaim{ID: "network-device"})
	if err != nil {
		t.Fatalf("LoginProfile: %v", err)
	}

	refuseByNetworkProvider(t, credentials.pool, accountID)
	if _, err := credentials.Authenticate(ctx, email, "profile-password", DeviceClaim{ID: "network-device"}); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("Authenticate = %v, want ErrNotPermitted", err)
	}
	if _, _, err := service.LoginProfile(ctx, email, "profile-password", DeviceClaim{ID: "network-device"}); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("LoginProfile = %v, want ErrNotPermitted", err)
	}
	if _, err := service.Refresh(ctx, open.RefreshToken); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("Refresh = %v, want ErrSessionRevoked", err)
	}
}
