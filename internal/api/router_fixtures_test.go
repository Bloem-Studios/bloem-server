package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

type loginTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type v1PINEnvelope struct {
	Valid        bool   `json:"valid"`
	ProfileToken string `json:"profile_token"`
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
		Env      map[string]string `yaml:"env"`
		Steps    []workflowStep    `yaml:"steps"`
		Strategy struct {
			Matrix struct {
				Shard []int `yaml:"shard"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
	}
	var workflow struct {
		Jobs map[string]workflowJob `yaml:"jobs"`
	}
	raw, err := os.ReadFile("../../.github/workflows/bloem-ci.yml")
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
		"Tenant and access-group migrations":         false,
		"Profile tenant persistence":                 false,
		"Resource tenancy, access and policy stores": false,
		"OPA tenant compatibility acceptance":        false,
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
		"go test -race ./internal/resourcetenancy ./internal/access ./internal/policy -count=1 -v -timeout=30m",
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

	// internal/tenancy's race tests run split across the tenant-stores
	// shards, each against its own disposable PostgreSQL, and together run
	// every test once: one shard per matrix entry, 1 to N.
	stores, ok := workflow.Jobs["tenant-stores"]
	if !ok {
		t.Fatal("CI has no tenant-stores race job")
	}
	if image := stores.Services["postgres"].Image; !strings.Contains(image, "pgvector") {
		t.Fatalf("tenant-stores postgres image = %q, want a pgvector-capable image", image)
	}
	if stores.Env["SILO_TEST_DATABASE_URL"] == "" || stores.Env["SILO_REQUIRE_TEST_DATABASE"] != "" {
		t.Fatalf("tenant-stores env = %v, want SILO_TEST_DATABASE_URL and no job-wide SILO_REQUIRE_TEST_DATABASE", stores.Env)
	}
	shards := stores.Strategy.Matrix.Shard
	for i, shard := range shards {
		if shard != i+1 {
			t.Fatalf("tenant-stores shards = %v, want 1 to N", shards)
		}
	}
	raced := false
	for _, step := range stores.Steps {
		if step.Name != "Tenancy store race tests" {
			continue
		}
		raced = true
		if step.Env["SILO_REQUIRE_TEST_DATABASE"] != "1" {
			t.Error("tenant-stores race step does not set SILO_REQUIRE_TEST_DATABASE=1")
		}
		for _, required := range []string{
			fmt.Sprintf(`scripts/ci/go-test-shard.sh ./internal/tenancy %d "$SHARD" -race`, len(shards)),
			`go test -race ./internal/tenancy -run "$tests" -count=1 -v -timeout=30m`,
		} {
			if !strings.Contains(step.Run, required) {
				t.Errorf("tenant-stores race step does not run %q", required)
			}
		}
	}
	if len(shards) == 0 || !raced {
		t.Fatal("tenant-stores does not run the tenancy race tests in shards")
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
	raw, err := os.ReadFile("../../.github/workflows/bloem-ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse CI workflow: %v", err)
	}
	goJob, ok := workflow.Jobs["go-lint"]
	if !ok {
		t.Fatal("CI has no go-lint job")
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
		// The step delegates to a script; the guarantees live in its body.
		body := step.Run
		if script := strings.TrimSpace(step.Run); strings.HasPrefix(script, "scripts/") && !strings.ContainsAny(script, " \n") {
			contents, err := os.ReadFile("../../" + script)
			if err != nil {
				t.Fatalf("read lint script: %v", err)
			}
			body += "\n" + string(contents)
		}
		// Only the public Silo repository may be fetched: checkout removed the
		// credential, so fetching origin (private) would fail.
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "git fetch") && !strings.Contains(line, "https://github.com/Silo-Server/silo-server.git") {
				t.Fatalf("lint script refetches a repository other than public Silo: %q", strings.TrimSpace(line))
			}
		}
		if !strings.Contains(body, "--new-from-rev") && !strings.Contains(body, "--new-from-merge-base") {
			t.Error("lint step does not scope findings to changed lines")
		}
		for _, required := range []string{"git cat-file -e"} {
			if !strings.Contains(body, required) {
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

func decodeLogin(t *testing.T, response *httptest.ResponseRecorder) loginTokens {
	t.Helper()
	var envelope loginTokens
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

func newV1TenancyDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool := newDisposableAPIDatabase(t, "bloem_tenancy_", false)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	enableProfilePasswords(t, pool)
	return pool
}
