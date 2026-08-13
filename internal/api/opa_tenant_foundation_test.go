package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
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
	if os.Getenv("SILO_TEST_DATABASE_URL") == "" {
		t.Fatal("SILO_TEST_DATABASE_URL is required for the OPA tenant compatibility acceptance")
	}

	ctx := context.Background()
	pool := newDisposableAPIDatabase(t, "vondel_opa_foundation_", true)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	if err := database.MigrateDownTo(ctx, pool, migrations.FS, "sql", opaTenantIdentityPredecessor); err != nil {
		t.Fatalf("migrate to tenant-identity predecessor: %v", err)
	}

	const (
		username        = "opa-foundation-owner"
		password        = "correct horse battery staple"
		legacyProfileID = "opa-legacy-profile"
		v2ProfileID     = "opa-v2-profile"
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
		INSERT INTO users (username, email, password_hash, role, enabled, access_group_id)
		VALUES ($1, 'opa-foundation-owner@example.test', $2, 'admin', true, $3)
		RETURNING id`, username, passwordHash, defaultGroupID).Scan(&accountID); err != nil {
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

	var foreignOrganizationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id)
		VALUES ('opa-foundation-foreign', 'OPA Foundation Foreign', 'active', $1)
		RETURNING id`, accountID).Scan(&foreignOrganizationID); err != nil {
		t.Fatalf("create second organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'admin')`, foreignOrganizationID, accountID); err != nil {
		t.Fatalf("create second organization membership: %v", err)
	}
	groups := access.NewGroupStore(pool)
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
		ID:             v2ProfileID,
		Name:           "OPA V2 Profile",
		OrganizationID: legacyTenant.OrganizationID.String(),
		AccessGroupID:  &defaultGroupID,
	}); err != nil {
		t.Fatalf("create native default-organization profile: %v", err)
	}
	if err := userStore.CreateProfile(ctx, userstore.Profile{
		ID:             foreignProfile,
		Name:           "OPA Foreign Profile",
		OrganizationID: foreignOrganizationID.String(),
		AccessGroupID:  &foreignGroup.ID,
	}); err != nil {
		t.Fatalf("create foreign organization profile: %v", err)
	}
	for profileID, value := range map[string]string{
		legacyProfileID: `"24h"`,
		v2ProfileID:     `"12h"`,
	} {
		if _, err := userStore.UpsertSettingValue(ctx, userstore.SettingIdentity{
			Key:       "ui.time_format",
			Scope:     settingscontract.ScopeProfile,
			ProfileID: profileID,
		}, json.RawMessage(value)); err != nil {
			t.Fatalf("seed profile-scoped setting for %q: %v", profileID, err)
		}
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
		AccountID:      accountID,
		ProfileID:      foreignProfile,
	})
	if err != nil || foreignPolicy == nil || foreignPolicy.ID != foreignGroup.ID || foreignPolicy.MaxPlaybackQuality != "1080p" {
		t.Fatalf("foreign organization profile policy = %#v, %v", foreignPolicy, err)
	}
	if _, err := groups.ResolvePolicy(ctx, access.GroupSubject{
		OrganizationID: legacyTenant.OrganizationID,
		AccountID:      accountID,
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
		MembershipProvisioner: bootstrap,
	})

	login := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"`+username+`","password":"`+password+`"}`, "", nil)
	if login.Code != http.StatusOK {
		t.Fatalf("v1 login = %d %s", login.Code, login.Body.String())
	}
	loginTokens := decodeLogin(t, login)
	assertLegacyToken(t, cfg, loginTokens.AccessToken)
	assertOPAAcceptanceV1Login(t, login, accountID, username)
	profiles := performJSONRequest(t, router, http.MethodGet, "/api/v1/profiles/", "", loginTokens.AccessToken, nil)
	assertOPAAcceptanceV1Profiles(t, profiles, map[string]string{
		legacyProfileID: "OPA Legacy Profile",
		v2ProfileID:     "OPA V2 Profile",
		foreignProfile:  "OPA Foreign Profile",
	}, legacyProfileID)
	verifyPIN := performJSONRequest(t, router, http.MethodPost, "/api/v1/profiles/"+legacyProfileID+"/verify-pin", `{"pin":"2468"}`, loginTokens.AccessToken, nil)
	profileToken := decodePIN(t, verifyPIN).ProfileToken
	profileSelection := performJSONRequest(t, router, http.MethodGet, "/api/v1/settings/values/effective?keys=ui.time_format", "", loginTokens.AccessToken, map[string]string{
		"X-Profile-Id":    legacyProfileID,
		"X-Profile-Token": profileToken,
	})
	switchedProfileSettings := performJSONRequest(t, router, http.MethodGet, "/api/v1/settings/values/effective?keys=ui.time_format", "", loginTokens.AccessToken, map[string]string{
		"X-Profile-Id": v2ProfileID,
	})
	assertV1Responses(t, profiles, verifyPIN, profileSelection, switchedProfileSettings)
	assertOPAAcceptanceV1ProfileSetting(t, profileSelection, legacyProfileID, "24h")
	assertOPAAcceptanceV1ProfileSetting(t, switchedProfileSettings, v2ProfileID, "12h")
	assertNoTenantFields(t, login, profiles, verifyPIN, profileSelection, switchedProfileSettings)

	visibleLibraries := performJSONRequest(t, router, http.MethodGet, "/api/v1/user/libraries", "", loginTokens.AccessToken, map[string]string{
		"X-Profile-Id":    legacyProfileID,
		"X-Profile-Token": profileToken,
	})
	if visibleLibraries.Code != http.StatusOK {
		t.Fatalf("v1 tenant-bounded libraries = %d %s", visibleLibraries.Code, visibleLibraries.Body.String())
	}
	var libraryResponse []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(visibleLibraries.Body.Bytes(), &libraryResponse); err != nil {
		t.Fatalf("decode tenant-bounded libraries: %v", err)
	}
	gotVisibleFolderIDs := make([]int, 0, len(libraryResponse))
	for _, folder := range libraryResponse {
		gotVisibleFolderIDs = append(gotVisibleFolderIDs, folder.ID)
	}
	slices.Sort(gotVisibleFolderIDs)
	if !slices.Equal(gotVisibleFolderIDs, wantVisibleFolderIDs) {
		t.Fatalf("router catalog scope = %v, want %v; foreign=%d non-entitled=%d must be absent", gotVisibleFolderIDs, wantVisibleFolderIDs, foreignFolderID, nonEntitledFolderID)
	}

	capabilities := performJSONRequest(t, router, http.MethodGet, "/api/v2/capabilities", "", "", nil)
	const wantCapabilities = `{"api":"v2","identity_schema":1,"features":{"legacy_silo_v1":true,"organization_memberships":true,"tenant_bounded_media_scope":true,"direct_profile_login":false,"shared_device_pairing":false,"delegated_admin_roles":false}}`
	if capabilities.Code != http.StatusOK || strings.TrimSpace(capabilities.Body.String()) != wantCapabilities {
		t.Errorf("v2 capabilities = %d %s, want exact implemented contract %s", capabilities.Code, strings.TrimSpace(capabilities.Body.String()), wantCapabilities)
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
		resourcetenancy.NewStore(pool),
		groups,
	)
	tenantMiddleware := apimw.NewTenantMiddleware(tenantResolver)
	viewerMiddleware := apimw.NewViewerAccessMiddleware(viewerResolver)
	v2SessionID := "opa-v2-adapter-session"
	v2Claims := opaAcceptanceTenantClaims(accountID, v2SessionID, nativeTenant)
	v2Adapter := tenantMiddleware.RequireV2(viewerMiddleware.RequireViewerAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := access.GetScope(r.Context())
		if !ok || !scope.LibrariesRestricted || !slices.Equal(scope.AllowedLibraryIDs, wantVisibleFolderIDs) {
			t.Errorf("v2 adapter scope = %#v, want exact tenant folders %v", scope, wantVisibleFolderIDs)
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	v2Request := httptest.NewRequest(http.MethodGet, "/api/v2/acceptance/viewer", nil)
	v2Request.Header.Set("X-Profile-Id", v2ProfileID)
	v2Request = v2Request.WithContext(apimw.SetClaims(v2Request.Context(), v2Claims))
	v2Response := httptest.NewRecorder()
	v2Adapter.ServeHTTP(v2Response, v2Request)
	if v2Response.Code != http.StatusNoContent {
		t.Fatalf("v2 adapter = %d %s", v2Response.Code, v2Response.Body.String())
	}

	staleGate := tenantMiddleware.RequireV2(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if _, err := pool.Exec(ctx, `
		UPDATE organization_memberships
		SET security_revision=security_revision+1
		WHERE id=$1`, nativeTenant.MembershipID); err != nil {
		t.Fatalf("advance membership revision: %v", err)
	}
	assertOPAAcceptanceStaleV2(t, staleGate, v2Claims, "membership revision")
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
	assertOPAAcceptanceStaleV2(t, staleGate, organizationStaleClaims, "organization revision")

	legacyClaims, err := auth.NewJWTService(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenExpiry, cfg.Auth.RefreshTokenExpiry).ValidateToken(loginTokens.AccessToken)
	if err != nil {
		t.Fatalf("decode v1 adapter session: %v", err)
	}
	decisionSessions := []string{legacyClaims.SessionID, v2SessionID}
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
	if !seenSessions[legacyClaims.SessionID] || !seenSessions[v2SessionID] {
		t.Fatalf("policy decision sessions = %#v, want v1 %q and v2 %q at generation %d", seenSessions, legacyClaims.SessionID, v2SessionID, policyGeneration)
	}
}

func newDisposableAPIDatabase(t *testing.T, prefix string, required bool) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if required {
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
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %q: %v", name, err)
		}
		var exists bool
		if err := admin.QueryRow(cleanupCtx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)`, name).Scan(&exists); err != nil {
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

func assertOPAAcceptanceStaleV2(t *testing.T, handler http.Handler, claims *auth.Claims, revision string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v2/acceptance/stale", nil)
	request = request.WithContext(apimw.SetClaims(request.Context(), claims))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"error":"authorization_state_stale"`) {
		t.Fatalf("stale %s response = %d %s, want authorization_state_stale", revision, response.Code, response.Body.String())
	}
}

func assertOPAAcceptanceV1Login(t *testing.T, response *httptest.ResponseRecorder, accountID int, username string) {
	t.Helper()
	var rawContract map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &rawContract); err != nil {
		t.Fatalf("decode v1 login field contract: %v", err)
	}
	assertOPAAcceptanceJSONFields(t, "v1 login", rawContract, "access_token", "expires_in", "refresh_token", "user")
	var rawUser map[string]json.RawMessage
	if err := json.Unmarshal(rawContract["user"], &rawUser); err != nil {
		t.Fatalf("decode v1 login user field contract: %v", err)
	}
	assertOPAAcceptanceJSONFields(t, "v1 login user", rawUser,
		"download_allowed", "email", "id", "permissions", "role", "username")

	var contract struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		User         struct {
			ID              int      `json:"id"`
			Username        string   `json:"username"`
			Email           string   `json:"email"`
			Role            string   `json:"role"`
			Permissions     []string `json:"permissions"`
			DownloadAllowed bool     `json:"download_allowed"`
		} `json:"user"`
	}
	decoder := json.NewDecoder(strings.NewReader(response.Body.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		t.Fatalf("decode exact v1 login contract: %v; body=%s", err, response.Body.String())
	}
	if contract.AccessToken == "" || contract.RefreshToken == "" || contract.ExpiresIn != 3600 {
		t.Fatalf("v1 login token contract = %#v, want non-empty tokens expiring in 3600 seconds", contract)
	}
	if contract.User.ID != accountID || contract.User.Username != username ||
		contract.User.Email != "opa-foundation-owner@example.test" || contract.User.Role != "admin" ||
		!contract.User.DownloadAllowed || !slices.Equal(contract.User.Permissions, []string{"marker_edit", "metadata_curation"}) {
		t.Fatalf("v1 login user = %#v, want migrated admin account %d/%q", contract.User, accountID, username)
	}
}

func assertOPAAcceptanceV1Profiles(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantNames map[string]string,
	protectedPrimaryID string,
) {
	t.Helper()
	var rawContract map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &rawContract); err != nil {
		t.Fatalf("decode v1 profile-list field contract: %v", err)
	}
	assertOPAAcceptanceJSONFields(t, "v1 profile list", rawContract, "avatar_upload_enabled", "profiles")
	var rawProfiles []map[string]json.RawMessage
	if err := json.Unmarshal(rawContract["profiles"], &rawProfiles); err != nil {
		t.Fatalf("decode v1 profile field contracts: %v", err)
	}
	for _, rawProfile := range rawProfiles {
		fields := []string{
			"allowed_library_ids", "auto_play_next_preview", "auto_skip_credits", "auto_skip_intro",
			"auto_skip_recap", "avatar_source", "created_at", "has_pin", "id", "is_child", "is_primary",
			"library_restrictions_enabled", "max_playback_quality", "name",
			"show_forced_subtitles", "subtitle_mode", "updated_at",
		}
		var profileID string
		if err := json.Unmarshal(rawProfile["id"], &profileID); err != nil {
			t.Fatalf("decode v1 profile id: %v", err)
		}
		if profileID == protectedPrimaryID {
			fields = append(fields, "quality_preference")
		}
		assertOPAAcceptanceJSONFields(t, "v1 profile "+profileID, rawProfile, fields...)
	}

	type profileContract struct {
		ID                         string `json:"id"`
		Name                       string `json:"name"`
		Avatar                     string `json:"avatar,omitempty"`
		AvatarURL                  string `json:"avatar_url,omitempty"`
		AvatarSource               string `json:"avatar_source,omitempty"`
		HasPIN                     bool   `json:"has_pin"`
		IsChild                    bool   `json:"is_child"`
		IsPrimary                  bool   `json:"is_primary"`
		MaxContentRating           string `json:"max_content_rating,omitempty"`
		QualityPreference          string `json:"quality_preference,omitempty"`
		Language                   string `json:"language,omitempty"`
		PreferredMetadataLanguage  string `json:"preferred_metadata_language,omitempty"`
		SubtitleLanguage           string `json:"subtitle_language,omitempty"`
		SubtitleMode               string `json:"subtitle_mode,omitempty"`
		AutoSkipIntro              bool   `json:"auto_skip_intro"`
		AutoSkipCredits            bool   `json:"auto_skip_credits"`
		AutoSkipRecap              bool   `json:"auto_skip_recap"`
		AutoPlayNextPreview        bool   `json:"auto_play_next_preview"`
		ShowForcedSubtitles        bool   `json:"show_forced_subtitles"`
		LibraryRestrictionsEnabled bool   `json:"library_restrictions_enabled"`
		AllowedLibraryIDs          []int  `json:"allowed_library_ids"`
		MaxPlaybackQuality         string `json:"max_playback_quality"`
		CreatedAt                  string `json:"created_at"`
		UpdatedAt                  string `json:"updated_at"`
	}
	var contract struct {
		Profiles            []profileContract `json:"profiles"`
		AvatarUploadEnabled bool              `json:"avatar_upload_enabled"`
	}
	decoder := json.NewDecoder(strings.NewReader(response.Body.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		t.Fatalf("decode exact v1 profile-list contract: %v; body=%s", err, response.Body.String())
	}
	if len(contract.Profiles) != len(wantNames) {
		t.Fatalf("v1 profiles = %#v, want exactly %d migrated/native profiles", contract.Profiles, len(wantNames))
	}
	seen := make(map[string]bool, len(contract.Profiles))
	for _, profile := range contract.Profiles {
		wantName, ok := wantNames[profile.ID]
		if !ok || profile.Name != wantName || profile.CreatedAt == "" || profile.UpdatedAt == "" {
			t.Fatalf("v1 profile = %#v, want one of %#v with timestamps", profile, wantNames)
		}
		if profile.ID == protectedPrimaryID && (!profile.HasPIN || !profile.IsPrimary) {
			t.Fatalf("migrated protected primary profile = %#v, want PIN-protected primary", profile)
		}
		seen[profile.ID] = true
	}
	for profileID := range wantNames {
		if !seen[profileID] {
			t.Fatalf("v1 profile list omitted %q: %#v", profileID, contract.Profiles)
		}
	}
}

func assertOPAAcceptanceV1ProfileSetting(
	t *testing.T,
	response *httptest.ResponseRecorder,
	profileID string,
	wantValue string,
) {
	t.Helper()
	var rawContract map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &rawContract); err != nil {
		t.Fatalf("decode v1 selected-profile settings field contract: %v", err)
	}
	assertOPAAcceptanceJSONFields(t, "v1 selected-profile settings", rawContract, "revision", "settings")
	var rawSettings []map[string]json.RawMessage
	if err := json.Unmarshal(rawContract["settings"], &rawSettings); err != nil {
		t.Fatalf("decode v1 selected-profile setting fields: %v", err)
	}
	if len(rawSettings) != 1 {
		t.Fatalf("v1 selected-profile setting field rows = %d, want 1", len(rawSettings))
	}
	assertOPAAcceptanceJSONFields(t, "v1 selected-profile setting", rawSettings[0],
		"definition_revision", "key", "profile_id", "scope", "source", "source_context", "updated_at", "value")
	var rawSourceContext map[string]json.RawMessage
	if err := json.Unmarshal(rawSettings[0]["source_context"], &rawSourceContext); err != nil {
		t.Fatalf("decode v1 selected-profile source-context fields: %v", err)
	}
	assertOPAAcceptanceJSONFields(t, "v1 selected-profile source context", rawSourceContext, "profile_id")

	var contract struct {
		Settings []struct {
			Key                string          `json:"key"`
			Value              json.RawMessage `json:"value"`
			Source             string          `json:"source"`
			DefinitionRevision int             `json:"definition_revision"`
			UpdatedAt          string          `json:"updated_at"`
			SourceContext      struct {
				ProfileID string `json:"profile_id"`
			} `json:"source_context"`
			Scope     string `json:"scope"`
			ProfileID string `json:"profile_id"`
		} `json:"settings"`
		Revision int `json:"revision"`
	}
	decoder := json.NewDecoder(strings.NewReader(response.Body.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		t.Fatalf("decode exact v1 selected-profile settings contract: %v; body=%s", err, response.Body.String())
	}
	if contract.Revision != 6 || len(contract.Settings) != 1 {
		t.Fatalf("v1 selected-profile settings = %#v, want revision 6 with one setting", contract)
	}
	setting := contract.Settings[0]
	if setting.Key != "ui.time_format" || string(setting.Value) != `"`+wantValue+`"` ||
		setting.Source != "profile" || setting.Scope != "profile" ||
		setting.ProfileID != profileID || setting.SourceContext.ProfileID != profileID ||
		setting.DefinitionRevision != contract.Revision || setting.UpdatedAt == "" {
		t.Fatalf("v1 selected profile setting = %#v, want profile %q value %q", setting, profileID, wantValue)
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
