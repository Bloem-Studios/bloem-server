package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const opaTenantIdentityPredecessor int64 = 20260812163547

// TestOPATenantFoundationWithDisposablePostgres is the release acceptance for
// the first tenant-authorization increment. Its expected folder and group IDs
// are captured from independently seeded rows, so removing either the tenant
// availability bound or organization-qualified group lookup makes it fail.
func TestOPATenantFoundationWithDisposablePostgres(t *testing.T) {
	ctx := context.Background()
	pool := newDisposableAPIDatabase(t, "bloem_opa_foundation_", true)
	if err := database.RunMigrations(ctx, pool, opaCompatibilityBaselineFS(t), "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	if err := database.MigrateDownTo(ctx, pool, opaCompatibilityBaselineFS(t), "sql", opaTenantIdentityPredecessor); err != nil {
		t.Fatalf("migrate to tenant-identity predecessor: %v", err)
	}

	const (
		username        = "opa-foundation-owner"
		password        = "correct horse battery staple"
		legacyProfileID = "opa-legacy-profile"
		bloemProfileID  = "opa-v2-profile"
		foreignProfile  = "opa-foreign-profile"
		sharedGroupName = "OPA Foundation Default"
	)

	var defaultGroupID int64
	if err := pool.QueryRow(ctx, `
		UPDATE access_groups
		SET name=$1, max_playback_quality='2160p'
		WHERE is_default
		RETURNING id`, sharedGroupName).Scan(&defaultGroupID); err != nil {
		t.Fatalf("prepare legacy default group: %v", err)
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash acceptance password: %v", err)
	}
	var accountID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ($1, 'opa-foundation-owner@example.test', $2, 'admin', true)
		RETURNING id`, username, passwordHash).Scan(&accountID); err != nil {
		t.Fatalf("create pre-existing account: %v", err)
	}
	pinHash, err := bcrypt.GenerateFromPassword([]byte("2468"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash profile PIN: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (user_id, id, name, pin_hash, is_primary)
		VALUES ($1, $2, 'OPA Legacy Profile', $3, true)`, accountID, legacyProfileID, pinHash); err != nil {
		t.Fatalf("create pre-existing profile: %v", err)
	}
	legacyEntitledFolderID := insertOPAAcceptanceFolder(t, ctx, pool, "OPA legacy entitled", nil)

	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate tenant foundation: %v", err)
	}
	enableProfilePasswords(t, pool)
	// Match a fresh application connection after the ID-widening migrations;
	// legacy prepared result types must not survive the schema transition.
	pool.Reset()

	tenantStore := tenancy.NewStore(pool)
	tenantResolver := tenancy.NewResolver(tenantStore)
	legacyTenant, err := tenantResolver.Resolve(ctx, accountID, nil, true)
	if err != nil {
		t.Fatalf("resolve pre-existing account through default organization: %v", err)
	}
	if !legacyTenant.Legacy || !legacyTenant.OrganizationDefault || legacyTenant.OrganizationID == uuid.Nil {
		t.Fatalf("legacy tenant = %#v, want resolved default organization", legacyTenant)
	}
	var migratedProfileOrganization uuid.UUID
	var migratedProfileGroupID int64
	if err := pool.QueryRow(ctx, `
		SELECT organization_id, access_group_id
		FROM user_profiles
		WHERE user_id=$1 AND id=$2`, accountID, legacyProfileID).Scan(&migratedProfileOrganization, &migratedProfileGroupID); err != nil {
		t.Fatalf("load migrated profile tenancy: %v", err)
	}
	if migratedProfileOrganization != legacyTenant.OrganizationID || migratedProfileGroupID != defaultGroupID {
		t.Fatalf("migrated profile tenant/group = %s/%d, want %s/%d", migratedProfileOrganization, migratedProfileGroupID, legacyTenant.OrganizationID, defaultGroupID)
	}

	var foreignAccountID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ('opa-foundation-foreign-owner', 'opa-foundation-foreign@example.test', $1, 'admin', true)
		RETURNING id`, passwordHash).Scan(&foreignAccountID); err != nil {
		t.Fatalf("create foreign organization account: %v", err)
	}
	var foreignOrganizationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id)
		VALUES ('opa-foundation-foreign', 'OPA Foundation Foreign', 'active', $1)
		RETURNING id`, foreignAccountID).Scan(&foreignOrganizationID); err != nil {
		t.Fatalf("create second organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
SELECT $1, $2, 'active', 'admin'
ON CONFLICT (organization_id, account_id) DO UPDATE
SET status = EXCLUDED.status, legacy_role = EXCLUDED.legacy_role`, foreignOrganizationID, foreignAccountID); err != nil {
		t.Fatalf("create second organization membership: %v", err)
	}
	groups := access.NewTenantGroupStore(pool)
	foreignGroup, err := groups.Create(ctx, foreignOrganizationID, access.CreateGroupInput{
		Name:               sharedGroupName,
		MaxPlaybackQuality: "1080p",
		IsDefault:          true,
	})
	if err != nil {
		t.Fatalf("create same-named foreign default group: %v", err)
	}
	var sameNamedDefaults int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM access_groups WHERE name=$1 AND is_default`, sharedGroupName).Scan(&sameNamedDefaults); err != nil {
		t.Fatalf("count same-named default groups: %v", err)
	}
	if sameNamedDefaults != 2 {
		t.Fatalf("same-named default groups = %d, want 2", sameNamedDefaults)
	}

	userStores := pgstore.NewPostgresProvider(pool)
	userStore, err := userStores.ForUser(ctx, accountID)
	if err != nil {
		t.Fatalf("open migrated user store: %v", err)
	}
	if err := userStore.CreateProfile(ctx, userstore.Profile{
		ID:             bloemProfileID,
		Name:           "OPA V2 Profile",
		OrganizationID: legacyTenant.OrganizationID.String(),
		AccessGroupID:  &defaultGroupID,
	}); err != nil {
		t.Fatalf("create native default-organization profile: %v", err)
	}
	foreignUserStore, err := userStores.ForUser(ctx, foreignAccountID)
	if err != nil {
		t.Fatalf("open foreign organization user store: %v", err)
	}
	if err := foreignUserStore.CreateProfile(ctx, userstore.Profile{
		ID:             foreignProfile,
		Name:           "OPA Foreign Profile",
		OrganizationID: foreignOrganizationID.String(),
		AccessGroupID:  &foreignGroup.ID,
	}); err != nil {
		t.Fatalf("create foreign organization profile: %v", err)
	}
	for profileID, value := range map[string]string{
		legacyProfileID: `"24h"`,
		bloemProfileID:  `"12h"`,
	} {
		if _, err := userStore.UpsertSettingValue(ctx, userstore.SettingIdentity{
			Key:       "ui.time_format",
			Scope:     settingscontract.ScopeProfile,
			ProfileID: profileID,
		}, json.RawMessage(value)); err != nil {
			t.Fatalf("seed profile-scoped setting for %q: %v", profileID, err)
		}
	}
	if _, err := foreignUserStore.UpsertSettingValue(ctx, userstore.SettingIdentity{
		Key:       "ui.time_format",
		Scope:     settingscontract.ScopeProfile,
		ProfileID: foreignProfile,
	}, json.RawMessage(`"foreign-only"`)); err != nil {
		t.Fatalf("seed foreign profile-scoped setting: %v", err)
	}
	defaultPolicy, err := groups.ResolvePolicy(ctx, access.GroupSubject{
		OrganizationID: legacyTenant.OrganizationID,
		AccountID:      accountID,
		ProfileID:      legacyProfileID,
	})
	if err != nil || defaultPolicy == nil || defaultPolicy.ID != defaultGroupID || defaultPolicy.MaxPlaybackQuality != "2160p" {
		t.Fatalf("default organization profile policy = %#v, %v", defaultPolicy, err)
	}
	foreignPolicy, err := groups.ResolvePolicy(ctx, access.GroupSubject{
		OrganizationID: foreignOrganizationID,
		AccountID:      foreignAccountID,
		ProfileID:      foreignProfile,
	})
	if err != nil || foreignPolicy == nil || foreignPolicy.ID != foreignGroup.ID || foreignPolicy.MaxPlaybackQuality != "1080p" {
		t.Fatalf("foreign organization profile policy = %#v, %v", foreignPolicy, err)
	}
	if _, err := groups.ResolvePolicy(ctx, access.GroupSubject{
		OrganizationID: legacyTenant.OrganizationID,
		AccountID:      foreignAccountID,
		ProfileID:      foreignProfile,
	}); !errors.Is(err, access.ErrGroupNotFound) {
		t.Fatalf("cross-organization profile policy error = %v, want ErrGroupNotFound", err)
	}

	defaultOwnerID := opaAcceptanceOrganizationOwner(t, ctx, pool, legacyTenant.OrganizationID)
	foreignOwnerID := opaAcceptanceOrganizationOwner(t, ctx, pool, foreignOrganizationID)
	ownedFolderID := insertOPAAcceptanceFolder(t, ctx, pool, "OPA organization owned", &defaultOwnerID)
	nonEntitledFolderID := insertOPAAcceptanceFolder(t, ctx, pool, "OPA platform non-entitled", nil)
	if _, err := pool.Exec(ctx, `
		DELETE FROM organization_entitlements
		WHERE organization_id=$1 AND media_folder_id=$2`, legacyTenant.OrganizationID, nonEntitledFolderID); err != nil {
		t.Fatalf("remove compatibility entitlement from non-entitled folder: %v", err)
	}
	foreignFolderID := insertOPAAcceptanceFolder(t, ctx, pool, "OPA foreign owned", &foreignOwnerID)
	wantVisibleFolderIDs := []int{legacyEntitledFolderID, ownedFolderID}
	slices.Sort(wantVisibleFolderIDs)
	availableFolderIDs, err := resourcetenancy.NewStore(pool).AvailableMediaFolderIDs(ctx, legacyTenant)
	if err != nil {
		t.Fatalf("resolve tenant folder availability: %v", err)
	}
	if !slices.Equal(availableFolderIDs, wantVisibleFolderIDs) {
		t.Fatalf("available folders = %v, want organization-owned plus entitled %v (excluding %d and %d)", availableFolderIDs, wantVisibleFolderIDs, nonEntitledFolderID, foreignFolderID)
	}

	decisionLogger := policy.NewDecisionLogger(
		pool,
		"opa-foundation-acceptance",
		policy.WithDecisionLogBatchSize(100),
		policy.WithDecisionLogFlushInterval(10*time.Millisecond),
	)
	decisionLogger.SetScopeSampleRate(1)
	policySystem := policy.NewSystem(
		policy.NewPolicyStore(pool),
		nil,
		slog.Default(),
		policy.WithSystemDecisionLogger(decisionLogger),
		policy.WithSystemPollInterval(time.Hour),
	)
	if err := policySystem.Start(ctx); err != nil {
		t.Fatalf("start acceptance policy system: %v", err)
	}
	policyStopped := false
	t.Cleanup(func() {
		if !policyStopped {
			policySystem.Stop()
		}
	})
	policyGeneration := policySystem.Generation()
	if policyGeneration <= 0 {
		t.Fatalf("policy generation = %d, want positive revision", policyGeneration)
	}

	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret:          "opa-foundation-acceptance-secret",
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}}
	bootstrap := v1TenancyBootstrap{store: tenantStore}
	router := NewRouter(Dependencies{
		AppContext:            ctx,
		DB:                    pool,
		Config:                cfg,
		FolderRepo:            catalog.NewFolderRepository(pool),
		UserStoreProvider:     userStores,
		PolicySystem:          policySystem,
		OwnershipBootstrapper: bootstrap,
	})

	login := performJSONRequest(t, router, http.MethodPost, "/api/v2/auth/login", `{"username":"`+username+`","password":"`+password+`"}`, "", nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	loginTokens := decodeLogin(t, login)
	assertLegacyToken(t, cfg, loginTokens.AccessToken)

	// The default organization's account sees its own two profiles and never
	// the foreign organization's.
	profiles := performJSONRequest(t, router, http.MethodGet, "/api/v2/profiles", "", loginTokens.AccessToken, nil)
	if profiles.Code != http.StatusOK ||
		!strings.Contains(profiles.Body.String(), legacyProfileID) || !strings.Contains(profiles.Body.String(), "OPA Legacy Profile") ||
		!strings.Contains(profiles.Body.String(), bloemProfileID) || !strings.Contains(profiles.Body.String(), "OPA V2 Profile") ||
		strings.Contains(profiles.Body.String(), foreignProfile) || strings.Contains(profiles.Body.String(), "OPA Foreign Profile") {
		t.Fatalf("profiles = %d %s, want exactly this organization's profiles", profiles.Code, profiles.Body.String())
	}
	verifyPIN := performJSONRequest(t, router, http.MethodPost, "/api/v2/profiles/"+legacyProfileID+"/verify-pin", `{"pin":"2468"}`, loginTokens.AccessToken, nil)
	if verifyPIN.Code != http.StatusOK {
		t.Fatalf("verify-pin = %d %s", verifyPIN.Code, verifyPIN.Body.String())
	}
	profileToken := decodePIN(t, verifyPIN).ProfileToken
	settingsFor := func(headers map[string]string) *httptest.ResponseRecorder {
		return performJSONRequest(t, router, http.MethodGet, "/api/v2/settings/values/effective?keys=ui.time_format", "", loginTokens.AccessToken, headers)
	}
	profileSelection := settingsFor(map[string]string{"X-Profile-Id": legacyProfileID, "X-Profile-Token": profileToken})
	switchedProfileSettings := settingsFor(map[string]string{"X-Profile-Id": bloemProfileID})
	foreignProfileSettings := settingsFor(map[string]string{"X-Profile-Id": foreignProfile})
	if foreignProfileSettings.Code < http.StatusBadRequest ||
		strings.Contains(foreignProfileSettings.Body.String(), foreignProfile) ||
		strings.Contains(foreignProfileSettings.Body.String(), "OPA Foreign Profile") ||
		strings.Contains(foreignProfileSettings.Body.String(), "foreign-only") {
		t.Fatalf("foreign profile settings = %d %s, want a non-disclosing refusal", foreignProfileSettings.Code, foreignProfileSettings.Body.String())
	}
	for _, check := range []struct {
		response *httptest.ResponseRecorder
		want     string
	}{{profileSelection, `"24h"`}, {switchedProfileSettings, `"12h"`}} {
		if check.response.Code != http.StatusOK || !strings.Contains(check.response.Body.String(), check.want) {
			t.Fatalf("selected-profile setting = %d %s, want %s", check.response.Code, check.response.Body.String(), check.want)
		}
	}
	assertNoTenantFields(t, login, profiles, verifyPIN, profileSelection, switchedProfileSettings, foreignProfileSettings)

	visibleLibraries := performJSONRequest(t, router, http.MethodGet, "/api/v2/user/libraries", "", loginTokens.AccessToken, map[string]string{
		"X-Profile-Id":    legacyProfileID,
		"X-Profile-Token": profileToken,
	})
	if visibleLibraries.Code != http.StatusOK {
		t.Fatalf("tenant-bounded libraries = %d %s", visibleLibraries.Code, visibleLibraries.Body.String())
	}
	var libraryResponse struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(visibleLibraries.Body.Bytes(), &libraryResponse); err != nil {
		t.Fatalf("decode tenant-bounded libraries: %v", err)
	}
	gotVisibleFolderIDs := make([]int, 0, len(libraryResponse.Items))
	for _, folder := range libraryResponse.Items {
		id, err := strconv.Atoi(folder.ID)
		if err != nil {
			t.Fatalf("library id %q: %v", folder.ID, err)
		}
		gotVisibleFolderIDs = append(gotVisibleFolderIDs, id)
	}
	slices.Sort(gotVisibleFolderIDs)
	if !slices.Equal(gotVisibleFolderIDs, wantVisibleFolderIDs) {
		t.Fatalf("router catalog scope = %v, want %v; foreign=%d non-entitled=%d must be absent", gotVisibleFolderIDs, wantVisibleFolderIDs, foreignFolderID, nonEntitledFolderID)
	}

	// The identity-model half of the capability document, which is what this
	// test owns: exactly these six booleans, exactly these values. The
	// byte-exact whole document — media types and feature tokens included — is
	// asserted in handlers.TestBloemCapabilitiesExactContract, so the literal is
	// not carried in two places where one copy would rot.
	capabilities := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/capabilities", "", "", nil)
	if capabilities.Code != http.StatusOK {
		t.Fatalf("v2 capabilities = %d %s", capabilities.Code, capabilities.Body.String())
	}
	var advertised struct {
		API            string          `json:"api"`
		IdentitySchema int             `json:"identity_schema"`
		Features       map[string]bool `json:"features"`
	}
	if err := json.Unmarshal(capabilities.Body.Bytes(), &advertised); err != nil {
		t.Fatalf("decode v2 capabilities %s: %v", capabilities.Body.String(), err)
	}
	wantFeatures := map[string]bool{
		"legacy_silo_v1": false, "organization_memberships": true, "tenant_bounded_media_scope": true,
		"direct_profile_login": false, "shared_device_pairing": false, "delegated_admin_roles": false,
	}
	featuresMatch := advertised.API == "bloem/v1" && advertised.IdentitySchema == 1 && len(advertised.Features) == len(wantFeatures)
	for name, want := range wantFeatures {
		if got, present := advertised.Features[name]; !present || got != want {
			featuresMatch = false
		}
	}
	if !featuresMatch {
		t.Errorf("v2 capabilities = %s, want api v2, identity_schema 1 and features %v", capabilities.Body.String(), wantFeatures)
	}
	for _, path := range []string{"/api/v10/capabilities", "/api/v10/organizations"} {
		response := performJSONRequest(t, router, http.MethodGet, path, "", loginTokens.AccessToken, nil)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, response.Code)
		}
	}

	nativeTenant, err := tenantResolver.Resolve(ctx, accountID, &legacyTenant.OrganizationID, false)
	if err != nil {
		t.Fatalf("resolve native default-organization tenant: %v", err)
	}
	viewerResolver := policy.NewViewerResolver(
		auth.NewUserRepository(pool),
		userStores,
		access.NewProfileTokenService(cfg.Auth.JWTSecret, 0),
		policySystem.PDP(),
		groups,
	).WithBloemTenancy(resourcetenancy.NewStore(pool))
	tenantMiddleware := apimw.NewTenantMiddleware(tenantResolver)
	viewerMiddleware := apimw.NewViewerAccessMiddleware(viewerResolver)
	bloemSessionID := "opa-v2-adapter-session"
	bloemClaims := opaAcceptanceTenantClaims(accountID, bloemSessionID, nativeTenant)
	bloemAdapter := tenantMiddleware.RequireBloem(viewerMiddleware.RequireViewerAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := access.GetScope(r.Context())
		if !ok || !scope.LibrariesRestricted || !slices.Equal(scope.AllowedLibraryIDs, wantVisibleFolderIDs) {
			t.Errorf("v2 adapter scope = %#v, want exact tenant folders %v", scope, wantVisibleFolderIDs)
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	bloemRequest := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/acceptance/viewer", nil)
	bloemRequest.Header.Set("X-Profile-Id", bloemProfileID)
	bloemRequest = bloemRequest.WithContext(apimw.SetClaims(bloemRequest.Context(), bloemClaims))
	bloemResponse := httptest.NewRecorder()
	bloemAdapter.ServeHTTP(bloemResponse, bloemRequest)
	if bloemResponse.Code != http.StatusNoContent {
		t.Fatalf("v2 adapter = %d %s", bloemResponse.Code, bloemResponse.Body.String())
	}

	staleGate := tenantMiddleware.RequireBloem(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if _, err := pool.Exec(ctx, `
		UPDATE organization_memberships
		SET security_revision=security_revision+1
		WHERE id=$1`, nativeTenant.MembershipID); err != nil {
		t.Fatalf("advance membership revision: %v", err)
	}
	assertOPAAcceptanceStaleBloem(t, staleGate, bloemClaims, "membership revision")
	nativeTenant, err = tenantResolver.Resolve(ctx, accountID, &legacyTenant.OrganizationID, false)
	if err != nil {
		t.Fatalf("re-resolve tenant after membership revision: %v", err)
	}
	organizationStaleClaims := opaAcceptanceTenantClaims(accountID, "opa-v2-stale-organization", nativeTenant)
	if _, err := pool.Exec(ctx, `
		UPDATE organizations
		SET policy_revision=policy_revision+1
		WHERE id=$1`, nativeTenant.OrganizationID); err != nil {
		t.Fatalf("advance organization revision: %v", err)
	}
	assertOPAAcceptanceStaleBloem(t, staleGate, organizationStaleClaims, "organization revision")

	legacyClaims, err := auth.NewJWTService(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenExpiry, cfg.Auth.RefreshTokenExpiry).ValidateToken(loginTokens.AccessToken)
	if err != nil {
		t.Fatalf("decode v1 adapter session: %v", err)
	}
	decisionSessions := []string{legacyClaims.SessionID, bloemSessionID}
	evidenceDeadline := time.Now().Add(10 * time.Second)
	for {
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(DISTINCT session_id)
			FROM policy_decisions
			WHERE user_id=$1
			  AND decision_name=$2
			  AND session_id=ANY($3::text[])`, accountID, string(policy.DecisionScope), decisionSessions).Scan(&count); err != nil {
			t.Fatalf("await v1/v2 policy evidence: %v", err)
		}
		if count == len(decisionSessions) {
			break
		}
		if time.Now().After(evidenceDeadline) {
			t.Fatalf("timed out awaiting v1/v2 policy evidence for sessions %q", decisionSessions)
		}
		time.Sleep(10 * time.Millisecond)
	}
	policySystem.Stop()
	policyStopped = true
	rows, err := pool.Query(ctx, `
		SELECT session_id, policy_generation
		FROM policy_decisions
		WHERE user_id=$1
		  AND decision_name=$2
		  AND session_id=ANY($3::text[])
		ORDER BY id`, accountID, string(policy.DecisionScope), decisionSessions)
	if err != nil {
		t.Fatalf("load v1/v2 policy evidence: %v", err)
	}
	defer rows.Close()
	seenSessions := map[string]bool{}
	for rows.Next() {
		var sessionID string
		var generation int64
		if err := rows.Scan(&sessionID, &generation); err != nil {
			t.Fatalf("scan v1/v2 policy evidence: %v", err)
		}
		seenSessions[sessionID] = true
		if generation != policyGeneration {
			t.Errorf("policy generation for adapter session %q = %d, want %d", sessionID, generation, policyGeneration)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate v1/v2 policy evidence: %v", err)
	}
	if !seenSessions[legacyClaims.SessionID] || !seenSessions[bloemSessionID] {
		t.Fatalf("policy decision sessions = %#v, want v1 %q and v2 %q at generation %d", seenSessions, legacyClaims.SessionID, bloemSessionID, policyGeneration)
	}
}

func newDisposableAPIDatabase(t *testing.T, prefix string, required bool) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if required && os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate disposable database name: %v", err)
	}
	name := prefix + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse maintenance database URL: %v", err)
	}
	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse disposable database URL: %v", err)
	}
	testConfig.ConnConfig.Database = name
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database %q: %v", name, err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		t.Fatalf("connect disposable database %q: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
		terminateCtx, cancelTerminate := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = admin.Exec(terminateCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		cancelTerminate()

		dropCtx, cancelDrop := context.WithTimeout(context.Background(), 30*time.Second)
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %q: %v", name, err)
		}
		cancelDrop()

		var exists bool
		verifyCtx, cancelVerify := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelVerify()
		if err := admin.QueryRow(verifyCtx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)`, name).Scan(&exists); err != nil {
			t.Errorf("verify disposable database %q cleanup: %v", name, err)
		} else if exists {
			t.Errorf("disposable database %q still exists after cleanup", name)
		}
		admin.Close()
	})
	return pool
}

func insertOPAAcceptanceFolder(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, ownerID *uuid.UUID) int {
	t.Helper()
	var id int
	if ownerID == nil {
		if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name) VALUES ('movies', $1) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatalf("create platform folder %q: %v", name, err)
		}
		return id
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, owner_id) VALUES ('movies', $1, $2) RETURNING id`, name, *ownerID).Scan(&id); err != nil {
		t.Fatalf("create organization folder %q: %v", name, err)
	}
	return id
}

func opaAcceptanceOrganizationOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, organizationID uuid.UUID) uuid.UUID {
	t.Helper()
	var ownerID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id FROM resource_owners
		WHERE kind='organization' AND organization_id=$1`, organizationID).Scan(&ownerID); err != nil {
		t.Fatalf("load organization %s resource owner: %v", organizationID, err)
	}
	return ownerID
}

func opaAcceptanceTenantClaims(accountID int, sessionID string, tenant tenancy.Context) *auth.Claims {
	return &auth.Claims{
		UserID:           accountID,
		SessionID:        sessionID,
		TokenType:        auth.TokenTypeAccess,
		OrganizationID:   tenant.OrganizationID.String(),
		MembershipID:     tenant.MembershipID.String(),
		PolicyRevision:   tenant.PolicyRevision,
		SecurityRevision: tenant.SecurityRevision,
	}
}

func assertOPAAcceptanceStaleBloem(t *testing.T, handler http.Handler, claims *auth.Claims, revision string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/acceptance/stale", nil)
	request = request.WithContext(apimw.SetClaims(request.Context(), claims))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"error":"authorization_state_stale"`) {
		t.Fatalf("stale %s response = %d %s, want authorization_state_stale", revision, response.Code, response.Body.String())
	}
}

func assertOPAAcceptanceJSONFields(
	t *testing.T,
	name string,
	got map[string]json.RawMessage,
	want ...string,
) {
	t.Helper()
	gotFields := make([]string, 0, len(got))
	for field := range got {
		gotFields = append(gotFields, field)
	}
	slices.Sort(gotFields)
	slices.Sort(want)
	if !slices.Equal(gotFields, want) {
		t.Fatalf("%s fields = %v, want exact normalized fields %v", name, gotFields, want)
	}
}

func opaCompatibilityBaselineFS(t *testing.T) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	if err := fs.WalkDir(migrations.FS, "sql", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, "_remove_bloem_compatibility.sql") ||
			strings.HasSuffix(path, "_remove_pastime_managed_tracking.sql") ||
			strings.HasSuffix(path, "_bloem_account_policy_on_users.sql") ||
			strings.HasSuffix(path, "_bloem_remove_music.sql") ||
			strings.HasSuffix(path, "_bloem_remove_lifecycle_receipts.sql") {
			return nil
		}
		data, err := migrations.FS.ReadFile(path)
		if err == nil {
			out[path] = &fstest.MapFile{Data: data}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
