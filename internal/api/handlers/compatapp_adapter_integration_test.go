package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/compatapp"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The optimistic-concurrency contract only exists if it survives the whole
// chain: the real handler, the real adapter, the real lifecycle service, and
// a real PostgreSQL row lock. A stub on either side of the seam would prove
// nothing about the 409 an administrator actually receives.

func TestCompatibilityAdminReturnsConflictForAStaleRevision(t *testing.T) {
	service := newCompatAdapterService(t)
	router := newCompatRouter(NewCompatApplicationService(service))
	mustEnrollCompanion(t, service, router, "inst-conflict")

	application := mustListCompatApplication(t, router, "inst-conflict")
	if application.Revision < 1 || !application.Enabled || application.State != "enabled" {
		t.Fatalf("application = %#v, want a freshly enrolled, enabled record", application)
	}

	// The decision the administrator was actually looking at succeeds.
	body := fmt.Sprintf(`{"expected_revision":%d}`, application.Revision)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-conflict/disable", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var disabled struct {
		Application CompatibilityApplication `json:"application"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &disabled); err != nil {
		t.Fatalf("decode disable response: %v", err)
	}
	if disabled.Application.Enabled || disabled.Application.Revision != application.Revision+1 {
		t.Fatalf("application = %#v, want it disabled one revision on", disabled.Application)
	}

	// The second administrator, still holding the page from before, is told
	// exactly what to reload against.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-conflict/enable", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale enable: got %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var conflict struct {
		Error           string `json:"error"`
		Message         string `json:"message"`
		CurrentRevision int64  `json:"current_revision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.Error != "authorization_state_changed" {
		t.Fatalf("error = %q, want authorization_state_changed", conflict.Error)
	}
	if conflict.CurrentRevision != disabled.Application.Revision {
		t.Fatalf("current_revision = %d, want %d", conflict.CurrentRevision, disabled.Application.Revision)
	}
	// The refused decision must not have landed.
	if current := mustListCompatApplication(t, router, "inst-conflict"); current.Enabled {
		t.Fatalf("application = %#v, want the refused enable to have changed nothing", current)
	}

	// Retrying against the revision the conflict named settles it.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-conflict/enable",
		fmt.Sprintf(`{"expected_revision":%d}`, conflict.CurrentRevision)))
	if rec.Code != http.StatusOK {
		t.Fatalf("retry enable: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestCompatibilityAdminRotatesAndRevokesThroughTheRealService(t *testing.T) {
	ctx := context.Background()
	service := newCompatAdapterService(t)
	router := newCompatRouter(NewCompatApplicationService(service))
	credential := mustEnrollCompanion(t, service, router, "inst-lifecycle")
	application := mustListCompatApplication(t, router, "inst-lifecycle")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-lifecycle/rotate-credential",
		fmt.Sprintf(`{"expected_revision":%d}`, application.Revision)))
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if store := rec.Header().Get("Cache-Control"); store != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store on a response carrying a secret", store)
	}
	var rotated struct {
		Credential  CompatibilityCredential  `json:"credential"`
		Application CompatibilityApplication `json:"application"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("decode rotation: %v", err)
	}
	if rotated.Credential.Secret == "" || rotated.Credential.Secret == credential.Secret {
		t.Fatal("rotation returned no fresh credential")
	}
	// A rotation is an administrative decision, so the next decision has to
	// be taken against the revision it produced.
	if rotated.Application.Revision != application.Revision+1 {
		t.Fatalf("revision = %d, want %d", rotated.Application.Revision, application.Revision+1)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-lifecycle/revoke",
		fmt.Sprintf(`{"expected_revision":%d,"confirmed":true}`, application.Revision)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale revoke: got %d, want 409: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-lifecycle/revoke",
		fmt.Sprintf(`{"expected_revision":%d,"confirmed":true}`, rotated.Application.Revision)))
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	revoked := mustListCompatApplication(t, router, "inst-lifecycle")
	if !revoked.Revoked || revoked.State != "revoked" {
		t.Fatalf("application = %#v, want a revoked record", revoked)
	}
	if _, err := service.Authenticate(ctx, rotated.Credential.Secret, nil); err == nil {
		t.Fatal("the rotated credential still authenticates after revocation")
	}

	// A decision against a revoked application answers the same conflict, so
	// the client's next move is the same: reload.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-lifecycle/enable",
		fmt.Sprintf(`{"expected_revision":%d}`, revoked.Revision)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("enable after revoke: got %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestCompatibilityAdminReportsAnUnknownInstance(t *testing.T) {
	service := newCompatAdapterService(t)
	router := newCompatRouter(NewCompatApplicationService(service))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/applications/inst-missing/disable", `{"expected_revision":1}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown instance: got %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestCompatibilityAdminRejectsAnUnreviewedEnrollment(t *testing.T) {
	service := newCompatAdapterService(t)
	router := newCompatRouter(NewCompatApplicationService(service))

	// The handler screens the kind itself; the capability vocabulary is the
	// service's, so this proves that refusal survives the seam.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/enrollments",
		`{"kind":"jellyfin","capabilities":["telepathy"]}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown capability: got %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

// mustEnrollCompanion drives the administrative half through the real HTTP
// surface and redeems the secret through the companion-facing service, which
// is exactly how the two halves meet in production.
func mustEnrollCompanion(t *testing.T, service *compatapp.Service, router http.Handler, instanceID string) compatapp.ServiceCredential {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodPost, "/enrollments",
		`{"kind":"jellyfin","capabilities":["catalog","playback"]}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create enrollment: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Enrollment CompatibilityEnrollment `json:"enrollment"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode enrollment: %v", err)
	}
	if created.Enrollment.Secret == "" || created.Enrollment.Kind != "jellyfin" {
		t.Fatalf("enrollment = %#v, want a jellyfin enrollment secret", created.Enrollment)
	}
	credential, err := service.Enroll(context.Background(), created.Enrollment.Secret, compatapp.EnrollmentRequest{
		InstanceID:   instanceID,
		Version:      "1.2.3",
		ImageDigest:  "sha256:0123456789abcdef",
		APIRangeMin:  1,
		APIRangeMax:  1,
		Capabilities: []compatapp.Capability{compatapp.CapabilityCatalog, compatapp.CapabilityPlayback},
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return credential
}

func mustListCompatApplication(t *testing.T, router http.Handler, instanceID string) CompatibilityApplication {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, compatRequest(t, http.MethodGet, "/applications", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("list applications: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Applications []struct {
			CompatibilityApplication
			CanonicalURL string `json:"canonical_url"`
		} `json:"applications"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode applications: %v", err)
	}
	for _, application := range response.Applications {
		if application.InstanceID == instanceID {
			if application.CanonicalURL == "" {
				t.Fatal("listed application carries no canonical URL")
			}
			return application.CompatibilityApplication
		}
	}
	t.Fatalf("no application with instance %q in %s", instanceID, rec.Body.String())
	return CompatibilityApplication{}
}

func newCompatAdapterService(t *testing.T) *compatapp.Service {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		// Skipping silently under the verification gate would let "go test"
		// report ok having executed nothing, which is how a broken seam stays
		// invisible. Same convention as internal/compatapp.
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newCompatAdapterDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	// A freshly migrated database is in the compatibility phase, which freezes
	// every policy write including the membership a new account is given.
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}
	return compatapp.NewService(pool)
}

// newCompatAdapterDisposableDatabase gives each test its own database, so a
// suite that races enrollment state cannot poison a shared one.
func newCompatAdapterDisposableDatabase(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate database name: %v", err)
	}
	name := "bloem_compatadmin_" + hex.EncodeToString(random[:])

	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database %q: %v", name, err)
	}

	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parse disposable database URL: %v", err)
	}
	testConfig.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		t.Fatalf("connect disposable database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		if err := admin.Ping(cleanupCtx); err == nil {
			if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
				t.Errorf("drop disposable database %q: %v", name, err)
			}
		}
		admin.Close()
	})
	return pool
}
