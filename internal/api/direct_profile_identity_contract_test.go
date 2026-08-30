package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/compatcontract"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// TestDirectProfileIdentityContractAgainstRealRouter drives the frozen
// direct-profile identity suite at the real v1 router over public HTTP,
// backed by a disposable PostgreSQL database. The suite's login case captures
// the credential the real login mints and the follow-up cases spend that same
// credential proving its least-privilege bounds; the revocation phases rotate
// real state — organization policy revision, membership security revision,
// and the profile credential itself — between picked sub-runs.
func TestDirectProfileIdentityContractAgainstRealRouter(t *testing.T) {
	ctx := context.Background()
	pool := newV1TenancyDatabase(t)
	bootstrap := v1TenancyBootstrap{store: tenancy.NewStore(pool)}
	router := NewRouter(Dependencies{
		DB: pool,
		Config: &config.Config{Auth: config.AuthConfig{
			JWTSecret:          "direct-profile-identity-contract-secret",
			AccessTokenExpiry:  time.Hour,
			RefreshTokenExpiry: 24 * time.Hour,
		}},
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
	})
	server := httptest.NewServer(router)
	defer server.Close()

	// Arrange the household over the public surface: an owner account, its
	// primary profile, and a Reader profile that carries direct credentials.
	setup := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/setup", `{
		"username":"owner","email":"owner@example.test","password":"correct horse battery staple",
		"create_default_profile":true,"default_profile_name":"Owner"
	}`, "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	accountToken := decodeLogin(t, setup).AccessToken
	var accountID int
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE username = 'owner'`).Scan(&accountID); err != nil {
		t.Fatalf("load owner: %v", err)
	}
	var primaryProfileID string
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 AND is_primary`, accountID).Scan(&primaryProfileID); err != nil {
		t.Fatalf("load primary profile: %v", err)
	}
	if created := performJSONRequest(t, router, http.MethodPost, "/api/v1/profiles/", `{"name":"Reader"}`, accountToken, nil); created.Code >= http.StatusBadRequest {
		t.Fatalf("create reader profile = %d %s", created.Code, created.Body.String())
	}
	var readerProfileID string
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 AND name = 'Reader'`, accountID).Scan(&readerProfileID); err != nil {
		t.Fatalf("load reader profile: %v", err)
	}
	credentials := auth.NewProfileCredentialService(pool)
	const directEmail = "direct-reader@example.test"
	directPassword := "fixture-direct-password-003"
	if err := credentials.Set(ctx, accountID, readerProfileID, directEmail, directPassword); err != nil {
		t.Fatalf("set direct credential: %v", err)
	}

	bindings := map[string]string{
		"direct_email":       directEmail,
		"direct_password":    directPassword,
		"wrong_password":     "fixture-wrong-password-002",
		"device_id":          "fixture-device-tablet-01",
		"direct_profile_id":  readerProfileID,
		"sibling_profile_id": primaryProfileID,
	}
	target := compatcontract.Target{BaseURL: server.URL, Client: server.Client(), Bindings: bindings}
	suite := compatcontract.DirectProfileIdentity()

	// Phase 1: the login chain and every privilege bound, over real HTTP.
	report, err := compatcontract.Run(ctx, target, suite.Pick(
		"direct profile login binds exactly one profile",
		"a wrong profile password is refused",
		"the profile directory is refused",
		"household session management is refused",
		"a sibling profile update is refused",
		"minting an account api key is refused",
		"verifying a profile pin is refused",
		"the bound profile reads its own record",
		"the bound profile updates its own record",
	))
	if err != nil {
		t.Fatalf("privilege phase: %v; report=%s", err, report.JSON())
	}

	// loginDirect mints a fresh direct session over public HTTP so each
	// revocation phase invalidates a token that was valid moments before.
	loginDirect := func(password string) string {
		t.Helper()
		response := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/profile-login",
			`{"email":"`+directEmail+`","password":"`+password+`","device_id":"fixture-device-tablet-01"}`, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("direct login = %d %s", response.Code, response.Body.String())
		}
		var body struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode direct login: %v", err)
		}
		return body.AccessToken
	}
	probeStillWorks := func(token, phase string) {
		t.Helper()
		response := performJSONRequest(t, router, http.MethodGet, "/api/v1/settings/effective?keys=player.playback_speed", "", token, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: control token = %d %s, want a working fresh session", phase, response.Code, response.Body.String())
		}
	}
	runRevocation := func(name string) {
		t.Helper()
		report, err := compatcontract.Run(ctx, target, suite.Pick(name))
		if err != nil {
			t.Fatalf("%s: %v; report=%s", name, err, report.JSON())
		}
	}

	// Phase 2: a token outlived by the organization's policy revision.
	bindings["stale_policy_token"] = loginDirect(directPassword)
	if _, err := pool.Exec(ctx, `UPDATE organizations SET policy_revision = policy_revision + 1 WHERE is_default`); err != nil {
		t.Fatalf("rotate policy revision: %v", err)
	}
	runRevocation("a stale organization policy revision is refused")
	probeStillWorks(loginDirect(directPassword), "policy revision")

	// Phase 3: a token outlived by the membership's security revision.
	bindings["stale_security_token"] = loginDirect(directPassword)
	if _, err := pool.Exec(ctx, `UPDATE organization_memberships SET security_revision = security_revision + 1 WHERE account_id = $1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, accountID); err != nil {
		t.Fatalf("rotate security revision: %v", err)
	}
	runRevocation("a stale membership security revision is refused")
	probeStillWorks(loginDirect(directPassword), "security revision")

	// Phase 4: a token outlived by its own credential. Rotating the profile
	// password revokes the profile's direct sessions in the same transaction.
	bindings["reset_credential_token"] = loginDirect(directPassword)
	rotatedPassword := "fixture-rotated-password-004"
	if err := credentials.Set(ctx, accountID, readerProfileID, directEmail, rotatedPassword); err != nil {
		t.Fatalf("rotate direct credential: %v", err)
	}
	runRevocation("a reset profile credential is refused")
	probeStillWorks(loginDirect(rotatedPassword), "credential rotation")
}
