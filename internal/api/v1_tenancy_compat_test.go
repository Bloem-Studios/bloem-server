package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

type v1TenancyBootstrap struct{ store *tenancy.Store }

func (b v1TenancyBootstrap) ActivateInitialOwnership(ctx context.Context, accountID int) error {
	_, err := b.store.ActivateInitialOwnership(ctx, accountID)
	return err
}

func (b v1TenancyBootstrap) ProvisionDefaultMembership(ctx context.Context, accountID int, legacyRole string) error {
	_, err := b.store.ProvisionDefaultMembership(ctx, accountID, legacyRole)
	return err
}

func (b v1TenancyBootstrap) ProvisionDefaultMembershipInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	accountID int,
	legacyRole string,
) (uuid.UUID, uuid.UUID, error) {
	membership, err := b.store.ProvisionDefaultMembershipInTransaction(ctx, tx, accountID, legacyRole)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return membership.OrganizationID, membership.ID, nil
}

func (b v1TenancyBootstrap) ActivateInitialOwnershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int) error {
	_, err := b.store.ActivateInitialOwnershipInTransaction(ctx, tx, accountID)
	return err
}

type v1LoginEnvelope struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type v1PINEnvelope struct {
	Valid        bool   `json:"valid"`
	ProfileToken string `json:"profile_token"`
}

func TestV1TenancyCompatibility(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	store := tenancy.NewStore(pool)
	bootstrap := v1TenancyBootstrap{store: store}
	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret:          "v1-tenancy-compatibility-secret",
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}}
	router := NewRouter(Dependencies{
		DB:                    pool,
		Config:                cfg,
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
	})

	setup := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/setup", `{
		"username":"owner","email":"owner@example.test","password":"correct horse battery staple",
		"create_default_profile":true,"default_profile_name":"Owner"
	}`, "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	setupLogin := decodeLogin(t, setup)
	assertLegacyToken(t, cfg, setupLogin.AccessToken)
	assertNoTenantFields(t, setup)

	var userID int
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE username = 'owner'`).Scan(&userID); err != nil {
		t.Fatalf("load setup owner: %v", err)
	}
	var profileID string
	if err := pool.QueryRow(context.Background(), `SELECT id FROM user_profiles WHERE user_id = $1 AND is_primary`, userID).Scan(&profileID); err != nil {
		t.Fatalf("load primary profile: %v", err)
	}
	pinHash, err := bcrypt.GenerateFromPassword([]byte("2468"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash PIN: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE user_profiles SET pin_hash = $1 WHERE user_id = $2 AND id = $3`, pinHash, userID, profileID); err != nil {
		t.Fatalf("set profile PIN: %v", err)
	}

	beforeLogin := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"owner","password":"correct horse battery staple"}`, "", nil)
	if beforeLogin.Code != http.StatusOK {
		t.Fatalf("login before tenant revision = %d %s", beforeLogin.Code, beforeLogin.Body.String())
	}
	beforeTokens := decodeLogin(t, beforeLogin)
	assertLegacyToken(t, cfg, beforeTokens.AccessToken)
	assertNoTenantFields(t, beforeLogin)
	beforeProfiles := performJSONRequest(t, router, http.MethodGet, "/api/v1/profiles/", "", beforeTokens.AccessToken, nil)
	beforePIN := performJSONRequest(t, router, http.MethodPost, "/api/v1/profiles/"+profileID+"/verify-pin", `{"pin":"2468"}`, beforeTokens.AccessToken, nil)
	beforePINToken := decodePIN(t, beforePIN)
	beforeSelection := performJSONRequest(t, router, http.MethodGet, "/api/v1/settings/effective?keys=player.playback_speed", "", beforeTokens.AccessToken, map[string]string{
		"X-Profile-Id":    profileID,
		"X-Profile-Token": beforePINToken.ProfileToken,
	})
	beforeAdmin := performJSONRequest(t, router, http.MethodGet, "/api/v1/admin/users", "", beforeTokens.AccessToken, nil)
	beforeRefresh := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/refresh", fmt.Sprintf(`{"refresh_token":%q}`, beforeTokens.RefreshToken), "", nil)
	assertV1Responses(t, beforeProfiles, beforePIN, beforeSelection, beforeAdmin, beforeRefresh)

	// Model tenant control-plane churn after backfill. Legacy account/profile
	// authority is unchanged, while current tenant revisions advance.
	if _, err := pool.Exec(context.Background(), `UPDATE organizations SET policy_revision = policy_revision + 1 WHERE is_default`); err != nil {
		t.Fatalf("advance organization policy revision: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE organization_memberships SET security_revision = security_revision + 1 WHERE account_id = $1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, userID); err != nil {
		t.Fatalf("advance membership security revision: %v", err)
	}

	afterLogin := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"owner","password":"correct horse battery staple"}`, "", map[string]string{"X-Organization-Id": uuid.NewString()})
	if afterLogin.Code != http.StatusOK {
		t.Fatalf("login after tenant revision = %d %s", afterLogin.Code, afterLogin.Body.String())
	}
	afterTokens := decodeLogin(t, afterLogin)
	assertLegacyToken(t, cfg, afterTokens.AccessToken)
	assertNoTenantFields(t, afterLogin)
	afterProfiles := performJSONRequest(t, router, http.MethodGet, "/api/v1/profiles/", "", afterTokens.AccessToken, map[string]string{"X-Organization-Id": uuid.NewString()})
	afterPIN := performJSONRequest(t, router, http.MethodPost, "/api/v1/profiles/"+profileID+"/verify-pin", `{"pin":"2468"}`, afterTokens.AccessToken, map[string]string{"X-Organization-Id": uuid.NewString()})
	afterPINToken := decodePIN(t, afterPIN)
	afterSelection := performJSONRequest(t, router, http.MethodGet, "/api/v1/settings/effective?keys=player.playback_speed", "", afterTokens.AccessToken, map[string]string{
		"X-Organization-Id": uuid.NewString(),
		"X-Profile-Id":      profileID,
		"X-Profile-Token":   afterPINToken.ProfileToken,
	})
	afterAdmin := performJSONRequest(t, router, http.MethodGet, "/api/v1/admin/users", "", afterTokens.AccessToken, map[string]string{"X-Organization-Id": uuid.NewString()})
	afterRefresh := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/refresh", fmt.Sprintf(`{"refresh_token":%q}`, afterTokens.RefreshToken), "", map[string]string{"X-Organization-Id": uuid.NewString()})
	assertV1Responses(t, afterProfiles, afterPIN, afterSelection, afterAdmin, afterRefresh)

	for name, pair := range map[string][2]*httptest.ResponseRecorder{
		"login":          {beforeLogin, afterLogin},
		"profile list":   {beforeProfiles, afterProfiles},
		"PIN unlock":     {beforePIN, afterPIN},
		"profile select": {beforeSelection, afterSelection},
		"admin gate":     {beforeAdmin, afterAdmin},
		"token refresh":  {beforeRefresh, afterRefresh},
	} {
		before := normalizedV1JSON(t, pair[0].Body.Bytes())
		after := normalizedV1JSON(t, pair[1].Body.Bytes())
		if !reflect.DeepEqual(before, after) {
			t.Errorf("%s changed across tenant revisions:\nbefore=%#v\nafter=%#v", name, before, after)
		}
	}

	// An unresolved ownership state blocks native selection but legacy default
	// resolution and v1 login/profile switching continue to work.
	if _, err := pool.Exec(context.Background(), `UPDATE organizations SET status = 'initializing', owner_account_id = NULL WHERE is_default`); err != nil {
		t.Fatalf("make ownership ambiguous: %v", err)
	}
	defaultOrganization, err := store.DefaultOrganization(context.Background())
	if err != nil {
		t.Fatalf("load ambiguous default: %v", err)
	}
	resolver := tenancy.NewResolver(store)
	if _, err := resolver.Resolve(context.Background(), userID, &defaultOrganization.ID, false); !errors.Is(err, tenancy.ErrOwnershipResolutionRequired) {
		t.Fatalf("native ambiguous resolution error = %v, want ErrOwnershipResolutionRequired", err)
	}
	if _, err := resolver.Resolve(context.Background(), userID, nil, true); err != nil {
		t.Fatalf("legacy resolution during ambiguity: %v", err)
	}
	legacyDuringAmbiguity := performJSONRequest(t, router, http.MethodGet, "/api/v1/profiles/", "", afterTokens.AccessToken, nil)
	if legacyDuringAmbiguity.Code != http.StatusOK {
		t.Fatalf("legacy profile list during ambiguity = %d %s", legacyDuringAmbiguity.Code, legacyDuringAmbiguity.Body.String())
	}

	capabilities := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/capabilities", "", "", nil)
	if capabilities.Code != http.StatusOK || !strings.Contains(capabilities.Body.String(), `"direct_profile_login":true`) {
		t.Fatalf("v2 capabilities = %d %s, want direct profile login advertised", capabilities.Code, capabilities.Body.String())
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		response := performJSONRequest(t, router, method, NativeAPIPrefix+"/organizations", `{}`, afterTokens.AccessToken, nil)
		if response.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/bloem/v1/organizations = %d, want 405", method, response.Code)
		}
	}
}

func TestBloemFoundationCIRequiresDisposablePostgres(t *testing.T) {
	type workflowStep struct {
		Name string         `yaml:"name"`
		Uses string         `yaml:"uses"`
		Run  string         `yaml:"run"`
		Env  map[string]any `yaml:"env"`
		With map[string]any `yaml:"with"`
	}
	type workflowJob struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
		Env   map[string]string `yaml:"env"`
		Steps []workflowStep    `yaml:"steps"`
	}
	var workflow struct {
		Jobs map[string]workflowJob `yaml:"jobs"`
	}
	raw, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse CI workflow: %v", err)
	}
	job, ok := workflow.Jobs["tenant-identity"]
	if !ok {
		t.Fatal("CI has no tenant-identity acceptance job")
	}
	postgres, ok := job.Services["postgres"]
	if !ok || postgres.Image == "" {
		t.Fatalf("tenant-identity postgres service = %#v", job.Services)
	}
	if !strings.Contains(postgres.Image, "pgvector") {
		t.Fatalf("tenant-identity postgres image = %q, want a pgvector-capable image", postgres.Image)
	}
	if job.Env["SILO_TEST_DATABASE_URL"] == "" {
		t.Fatal("tenant-identity job does not supply SILO_TEST_DATABASE_URL")
	}
	if job.Env["SILO_REQUIRE_TEST_DATABASE"] != "" {
		t.Fatal("tenant-identity job must not require PostgreSQL job-wide")
	}

	var commands string
	checkoutLocked := false
	postgresSteps := map[string]bool{
		"Tenant and access-group migrations":  false,
		"Profile tenant persistence":          false,
		"Tenant resource and policy stores":   false,
		"OPA tenant compatibility acceptance": false,
	}
	for _, step := range job.Steps {
		commands += "\n" + step.Run
		if strings.HasPrefix(step.Uses, "actions/checkout@") && step.With["persist-credentials"] == false {
			checkoutLocked = true
		}
		if _, required := postgresSteps[step.Name]; required {
			postgresSteps[step.Name] = step.Env["SILO_REQUIRE_TEST_DATABASE"] == 1 || step.Env["SILO_REQUIRE_TEST_DATABASE"] == "1"
		}
	}
	if !checkoutLocked {
		t.Fatal("tenant-identity checkout must disable persisted credentials")
	}
	for _, required := range []string{
		"go test ./internal/database -run 'TestTenantIdentityMigration|TestResourceTenancyMigration|TestOrganizationAccessGroupMigration|TestProfileAccessGroupRequired' -count=1 -v -timeout=30m",
		"go test ./internal/userstore/pgstore -run 'TestProfileOrganizationAndAccessGroupPersistence|TestProfileAccessGroupRejectsDifferentOrganization' -count=1 -v -timeout=30m",
		"go test -race ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy -count=1 -v -timeout=30m",
		"go test ./internal/api -run 'TestV1TenancyCompatibility|TestOPATenantFoundationWithDisposablePostgres' -count=1 -v -timeout=30m",
	} {
		if !strings.Contains(commands, required) {
			t.Errorf("tenant-identity CI does not run %q", required)
		}
	}
	for step, signaled := range postgresSteps {
		if !signaled {
			t.Errorf("tenant-identity CI step %q does not set SILO_REQUIRE_TEST_DATABASE=1", step)
		}
	}
}

func TestCIChangedLineLintNeedsNoRepositoryCredential(t *testing.T) {
	type workflowStep struct {
		Name string         `yaml:"name"`
		Run  string         `yaml:"run"`
		Env  map[string]any `yaml:"env"`
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []workflowStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	raw, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse CI workflow: %v", err)
	}
	goJob, ok := workflow.Jobs["go"]
	if !ok {
		t.Fatal("CI has no go job")
	}
	for _, step := range goJob.Steps {
		if step.Name != "Lint changed lines" {
			continue
		}
		if strings.Contains(step.Run, "git fetch") {
			t.Fatal("lint step refetches the private repository after checkout credentials were removed")
		}
		baseSHA := fmt.Sprint(step.Env["BASE_SHA"])
		if !strings.Contains(baseSHA, "pull_request.base.sha") ||
			!strings.Contains(baseSHA, "github.event.before") {
			t.Fatalf("lint BASE_SHA = %q, want event-owned PR and push revisions", baseSHA)
		}
		for _, required := range []string{"git cat-file -e", "--new-from-merge-base"} {
			if !strings.Contains(step.Run, required) {
				t.Errorf("lint step does not contain %q", required)
			}
		}
		return
	}
	t.Fatal("CI has no changed-line lint step")
}

func assertLegacyToken(t *testing.T, cfg *config.Config, token string) {
	t.Helper()
	claims, err := auth.NewJWTService(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenExpiry, cfg.Auth.RefreshTokenExpiry).ValidateToken(token)
	if err != nil {
		t.Fatalf("validate legacy token: %v", err)
	}
	if claims.OrganizationID != "" || claims.MembershipID != "" || claims.PolicyRevision != 0 || claims.SecurityRevision != 0 {
		t.Fatalf("v1 token contains tenant authority: %#v", claims)
	}
}

func assertV1Responses(t *testing.T, responses ...*httptest.ResponseRecorder) {
	t.Helper()
	for _, response := range responses {
		if response.Code != http.StatusOK {
			t.Fatalf("v1 response = %d %s", response.Code, response.Body.String())
		}
	}
	assertNoTenantFields(t, responses...)
}

func assertNoTenantFields(t *testing.T, responses ...*httptest.ResponseRecorder) {
	t.Helper()
	for _, response := range responses {
		for _, forbidden := range []string{"organization_id", "membership_id", "policy_revision", "security_revision"} {
			if strings.Contains(response.Body.String(), `"`+forbidden+`"`) {
				t.Errorf("v1 response leaked tenant field %q: %s", forbidden, response.Body.String())
			}
		}
	}
}

func decodeLogin(t *testing.T, response *httptest.ResponseRecorder) v1LoginEnvelope {
	t.Helper()
	var envelope v1LoginEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if envelope.AccessToken == "" || envelope.RefreshToken == "" {
		t.Fatalf("login tokens missing: %s", response.Body.String())
	}
	return envelope
}

func decodePIN(t *testing.T, response *httptest.ResponseRecorder) v1PINEnvelope {
	t.Helper()
	var envelope v1PINEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode PIN response: %v", err)
	}
	if !envelope.Valid || envelope.ProfileToken == "" {
		t.Fatalf("PIN response missing valid profile token: %s", response.Body.String())
	}
	return envelope
}

func performJSONRequest(t *testing.T, handler http.Handler, method, path, body, accessToken string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func normalizedV1JSON(t *testing.T, raw []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode v1 JSON %s: %v", raw, err)
	}
	stripDynamicV1Fields(value)
	return value
}

func stripDynamicV1Fields(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for _, field := range []string{"access_token", "refresh_token", "profile_token", "expires_at", "last_login_at"} {
			delete(typed, field)
		}
		for _, child := range typed {
			stripDynamicV1Fields(child)
		}
	case []any:
		for _, child := range typed {
			stripDynamicV1Fields(child)
		}
	}
}

func newV1TenancyDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool := newDisposableAPIDatabase(t, "bloem_tenancy_", false)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	// A freshly migrated database is in the compatibility phase, which freezes
	// every policy write including the membership a new account is given.
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}
	return pool
}
