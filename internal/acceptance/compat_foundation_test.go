//go:build integration

// Foundation acceptance for the removable compatibility applications
// (Tasks 1-6 of docs/superpowers/plans/2026-08-13-vondel-compatibility-1-foundation.md).
//
// WHAT THIS DRIVES. One disposable PostgreSQL database, the real migrations,
// the real enrollment/trust service (internal/compatapp), the real private
// compatibility API (internal/compatapi) mounted through the production router
// seam (api.Dependencies.CompatAPIV1), the real fixed-path gateway
// (internal/compatgateway) composed exactly as cmd/silo composes the public
// port, and the real v1 identity surface (Task 1). Every behavioral assertion
// below goes over real HTTP through one composed public listener; nothing
// reaches a store directly except to arrange a precondition or to read back
// the state a refusal was supposed to leave alone.
//
// TWO PRODUCTION SEAMS ARE NOT FILLED YET, and this file must not pretend
// otherwise:
//
//  1. cmd/silo never sets api.Dependencies.CompatAPIV1, so the private
//     compatibility API is unreachable in a running server today. The mount
//     itself is production code (internal/api/router.go), and this file drives
//     it through that mount, but the composition root does not construct the
//     handler. Recorded as a defect rather than worked around.
//  2. cmd/silo constructs the gateway with a nil Config.States, with the reason
//     recorded at the call site: nothing records where a companion listens. So
//     the gateway answers compatibility_unavailable for every owned family in
//     a running server. This file supplies the provider the deployment contract
//     implies — real application state read back from the real trust service,
//     endpoints resolved by kind — because otherwise no routing, isolation, or
//     revocation behavior can be observed at all.
//
// Everything else is production wiring, and reverting it turns cases here red;
// the RED evidence for each is in the task report.
package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/compatapi"
	"github.com/Silo-Server/silo-server/internal/compatapp"
	"github.com/Silo-Server/silo-server/internal/compatgateway"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

const (
	foundationJWTSecret     = "compat-foundation-acceptance-secret"
	foundationAccountUser   = "foundation-owner"
	foundationAccountPass   = "correct horse battery staple"
	foundationProfileEmail  = "reader@foundation.example.test"
	foundationProfilePass   = "reader-direct-password"
	foundationProfileDevice = "reader-tablet"
	foundationPIN           = "2468"
)

// --- fake companions --------------------------------------------------------

// companionRequest is one request a fake companion observed. The gateway's
// contract is about what crosses the boundary, so the recording keeps the
// forwarded path in both its decoded and escaped forms plus the two headers
// the gateway mints.
type companionRequest struct {
	Method   string
	Path     string
	RawPath  string
	Query    string
	Identity string
	Trace    string
	Host     string
}

type fakeCompanion struct {
	name    string
	server  *httptest.Server
	mu      sync.Mutex
	seen    []companionRequest
	stopped bool
}

func newFakeCompanion(name string) *fakeCompanion {
	companion := &fakeCompanion{name: name}
	companion.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		companion.mu.Lock()
		companion.seen = append(companion.seen, companionRequest{
			Method:   r.Method,
			Path:     r.URL.Path,
			RawPath:  r.URL.EscapedPath(),
			Query:    r.URL.RawQuery,
			Identity: r.Header.Get("X-Vondel-Internal-Identity"),
			Trace:    r.Header.Get("X-Vondel-Trace-Id"),
			Host:     r.Host,
		})
		companion.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"companion": companion.name,
			"path":      r.URL.Path,
		})
	}))
	return companion
}

func (c *fakeCompanion) requests() []companionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]companionRequest, len(c.seen))
	copy(out, c.seen)
	return out
}

func (c *fakeCompanion) last(t *testing.T) companionRequest {
	t.Helper()
	seen := c.requests()
	if len(seen) == 0 {
		t.Fatalf("%s companion received no request", c.name)
	}
	return seen[len(seen)-1]
}

func (c *fakeCompanion) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seen)
}

func (c *fakeCompanion) stop() {
	c.mu.Lock()
	already := c.stopped
	c.stopped = true
	c.mu.Unlock()
	if !already {
		c.server.Close()
	}
}

func (c *fakeCompanion) endpoint(t *testing.T) *url.URL {
	t.Helper()
	parsed, err := url.Parse(c.server.URL)
	if err != nil {
		t.Fatalf("parse %s companion URL: %v", c.name, err)
	}
	return parsed
}

// --- gateway state provider -------------------------------------------------

// foundationStates is the gateway's StateProvider. Every lifecycle fact comes
// from the real trust service, so enabling, disabling, revoking, and health
// reporting are observed through the gateway exactly as an operator's actions
// would be. Only the endpoint is supplied here, because nothing in the server
// records where a companion listens yet — the gap cmd/silo documents at its
// nil States.
type foundationStates struct {
	apps      *compatapp.Service
	endpoints map[compatgateway.AppKind]*url.URL
}

func (s foundationStates) ApplicationStatus(ctx context.Context, kind compatgateway.AppKind) (compatgateway.Status, error) {
	applications, err := s.apps.ListApplications(ctx)
	if err != nil {
		return compatgateway.Status{}, err
	}
	for _, application := range applications {
		if string(application.Kind) != string(kind) {
			continue
		}
		return compatgateway.Status{
			Known:    true,
			Enrolled: true,
			Enabled:  application.Enabled,
			Revoked:  application.RevokedAt != nil,
			Healthy:  application.Health == compatapp.HealthHealthy,
			APICompatible: application.APIRangeMin <= compatapp.ServerAPIVersion &&
				application.APIRangeMax >= compatapp.ServerAPIVersion,
			Endpoint: s.endpoints[kind],
		}, nil
	}
	return compatgateway.Status{}, nil
}

// --- subject provider for the signed-cursor surface -------------------------

// foundationSubjects exists for exactly one reason: the handler resolves and
// revalidates a subject before any list operation runs, and no production
// SubjectService adapter exists yet (see the file header). Without it the
// signed-cursor contract cannot be reached at all. It asserts nothing and is
// never used to prove an identity, policy, or revocation property — those are
// all proven against the real v1 surface below.
type foundationSubjects struct{ subject compatapi.Subject }

func (s foundationSubjects) LoginDirect(context.Context, string, string, compatapi.DeviceClaim) (compatapi.Subject, error) {
	return compatapi.Subject{}, compatapi.ErrUnavailable
}

func (s foundationSubjects) LoginAccount(context.Context, string, string, compatapi.DeviceClaim) (compatapi.Subject, error) {
	return compatapi.Subject{}, compatapi.ErrUnavailable
}

func (s foundationSubjects) CurrentSubject(_ context.Context, subject compatapi.Subject) (compatapi.Subject, error) {
	return subject, nil
}

func (s foundationSubjects) DeviceProfiles(context.Context, compatapi.Subject) ([]compatapi.ProfileTile, error) {
	return nil, compatapi.ErrUnavailable
}

func (s foundationSubjects) VerifyPIN(context.Context, compatapi.Subject, string, string) error {
	return compatapi.ErrUnavailable
}

func (s foundationSubjects) SwitchProfile(context.Context, compatapi.Subject, string, string) (compatapi.Subject, error) {
	return compatapi.Subject{}, compatapi.ErrUnavailable
}

func (s foundationSubjects) Logout(context.Context, compatapi.Subject) error {
	return compatapi.ErrUnavailable
}

// foundationCatalog returns one deterministic page carrying a domain
// continuation token, so the handler has something real to sign.
type foundationCatalog struct{}

func (foundationCatalog) Libraries(context.Context, compatapi.Subject) ([]compatapi.Library, error) {
	return []compatapi.Library{{ID: "lib-1", Name: "Books", Kind: "audiobooks"}}, nil
}

func (foundationCatalog) Items(_ context.Context, _ compatapi.Subject, q compatapi.ItemQuery) (compatapi.ItemPage, error) {
	if q.Cursor == "" {
		return compatapi.ItemPage{
			Items:      []compatapi.Item{{ID: "item-1", Kind: "book", Title: "First"}},
			NextCursor: "domain-page-2",
		}, nil
	}
	return compatapi.ItemPage{Items: []compatapi.Item{{ID: "item-2", Kind: "book", Title: "Second"}}}, nil
}

func (foundationCatalog) Item(context.Context, compatapi.Subject, string) (compatapi.Item, error) {
	return compatapi.Item{}, compatapi.ErrNotFound
}

func (foundationCatalog) Children(context.Context, compatapi.Subject, string, compatapi.ItemQuery) (compatapi.ItemPage, error) {
	return compatapi.ItemPage{}, compatapi.ErrNotFound
}

func (foundationCatalog) Search(context.Context, compatapi.Subject, compatapi.SearchQuery) (compatapi.ItemPage, error) {
	return compatapi.ItemPage{}, compatapi.ErrNotFound
}

func (foundationCatalog) Person(context.Context, compatapi.Subject, string) (compatapi.Person, error) {
	return compatapi.Person{}, compatapi.ErrNotFound
}

func (foundationCatalog) ArtworkGrant(context.Context, compatapi.Subject, string, string, string) (compatapi.DeliveryGrant, error) {
	return compatapi.DeliveryGrant{}, compatapi.ErrNotFound
}

// --- fixture ----------------------------------------------------------------

type foundationFixture struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	apps   *compatapp.Service
	admin  handlers.CompatibilityApplicationService
	cfg    *config.Config
	public *httptest.Server

	jellyfin       *fakeCompanion
	audiobookshelf *fakeCompanion

	client *http.Client
}

type foundationBootstrap struct{ store *tenancy.Store }

func (b foundationBootstrap) ActivateInitialOwnership(ctx context.Context, accountID int) error {
	_, err := b.store.ActivateInitialOwnership(ctx, accountID)
	return err
}

func (b foundationBootstrap) ProvisionDefaultMembership(ctx context.Context, accountID int, legacyRole string) error {
	_, err := b.store.ProvisionDefaultMembership(ctx, accountID, legacyRole)
	return err
}

func newFoundationFixture(t *testing.T) *foundationFixture {
	t.Helper()
	ctx := context.Background()
	pool := newFoundationDatabase(t)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}

	apps := compatapp.NewService(pool)
	admin := handlers.NewCompatApplicationService(apps)
	if admin == nil {
		t.Fatal("the production admin adapter refused to wrap a real enrollment service")
	}

	compatHandler, err := compatapi.New(compatapi.Config{
		SubjectTokenKey: bytes.Repeat([]byte{0x5A}, 32),
		CursorKey:       bytes.Repeat([]byte{0xA5}, 32),
		Version:         "foundation-acceptance",
		// The range the private API advertises is the server's own API
		// version, so a bump to compatapp.ServerAPIVersion that nobody
		// propagates shows up here.
		APIRange: compatapi.APIRange{Min: compatapp.ServerAPIVersion, Max: compatapp.ServerAPIVersion},
	}, foundationCompatServices(apps))
	if err != nil {
		t.Fatalf("build private compatibility API: %v", err)
	}

	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret:          foundationJWTSecret,
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}}
	bootstrap := foundationBootstrap{store: tenancy.NewStore(pool)}
	router := api.NewRouter(api.Dependencies{
		AppContext:            ctx,
		DB:                    pool,
		Config:                cfg,
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
		FolderRepo:            catalog.NewFolderRepository(pool),
		CompatAPIV1:           compatHandler,
		CompatApplications:    admin,
	})

	jellyfin := newFakeCompanion("jellyfin")
	audiobookshelf := newFakeCompanion("audiobookshelf")
	t.Cleanup(jellyfin.stop)
	t.Cleanup(audiobookshelf.stop)

	gateway := compatgateway.New(compatgateway.Config{
		States: foundationStates{
			apps: apps,
			endpoints: map[compatgateway.AppKind]*url.URL{
				compatgateway.KindJellyfin:       jellyfin.endpoint(t),
				compatgateway.KindAudiobookshelf: audiobookshelf.endpoint(t),
			},
		},
		IdentitySecret: []byte(foundationJWTSecret),
		// One retry is enough for the acceptance: with a threshold of one the
		// stopped-companion case would open the circuit on its first failure
		// and the next assertion would see a 503 it did not cause.
		FailureThreshold: 50,
	})

	// The public listener, composed the way cmd/silo's publicMux composes it:
	// only /api/** reaches the chi router, and everything else goes through
	// the gateway's frontend fallback. That composition in package main is
	// pinned by the go/types guards in cmd/silo/main_test.go; this is the same
	// exported constructor those guards require main to call.
	frontend := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "<!doctype html><title>vondel</title>")
	})
	mux := http.NewServeMux()
	mux.Handle("/api/", router)
	mux.Handle("/", compatgateway.WithFrontendFallback(gateway, frontend))
	public := httptest.NewServer(mux)
	t.Cleanup(public.Close)

	return &foundationFixture{
		t:              t,
		ctx:            ctx,
		pool:           pool,
		apps:           apps,
		admin:          admin,
		cfg:            cfg,
		public:         public,
		jellyfin:       jellyfin,
		audiobookshelf: audiobookshelf,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// foundationCompatServices is the production seam (compatapi.CompatAppServices
// over the real compatapp.Service) plus the two providers the file header
// records as missing in production.
func foundationCompatServices(apps *compatapp.Service) compatapi.Services {
	services := compatapi.CompatAppServices(apps)
	services.Subjects = foundationSubjects{}
	services.Catalog = foundationCatalog{}
	return services
}

// --- HTTP helpers -----------------------------------------------------------

type foundationResponse struct {
	Status  int
	Header  http.Header
	Body    []byte
	Request string
}

func (f *foundationFixture) do(method, path, body string, headers map[string]string) foundationResponse {
	f.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(f.ctx, method, f.public.URL+path, reader)
	if err != nil {
		f.t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := f.client.Do(request)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatalf("read %s %s body: %v", method, path, err)
	}
	return foundationResponse{
		Status:  response.StatusCode,
		Header:  response.Header.Clone(),
		Body:    payload,
		Request: method + " " + path,
	}
}

func (f *foundationFixture) requireStatus(response foundationResponse, want int) foundationResponse {
	f.t.Helper()
	if response.Status != want {
		f.t.Fatalf("%s = %d %s, want %d", response.Request, response.Status, response.Body, want)
	}
	return response
}

// object decodes a response as raw JSON. Decoding into a production struct
// keeps passing after a field stops being populated, so every contract
// assertion below reads the map and pins the key set.
func (f *foundationFixture) object(response foundationResponse) map[string]any {
	f.t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(response.Body, &decoded); err != nil {
		f.t.Fatalf("%s body is not a JSON object: %v (%s)", response.Request, err, response.Body)
	}
	return decoded
}

func foundationKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (f *foundationFixture) requireKeys(what string, object map[string]any, want ...string) {
	f.t.Helper()
	sort.Strings(want)
	got := foundationKeys(object)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		f.t.Fatalf("%s key set = %v, want exactly %v", what, got, want)
	}
}

func (f *foundationFixture) requireErrorEnvelope(response foundationResponse, wantStatus int, wantCode string) {
	f.t.Helper()
	f.requireStatus(response, wantStatus)
	envelope := f.object(response)
	f.requireKeys(response.Request+" error envelope", envelope, "error")
	detail, ok := envelope["error"].(map[string]any)
	if !ok {
		f.t.Fatalf("%s error is %T, want an object", response.Request, envelope["error"])
	}
	f.requireKeys(response.Request+" error detail", detail, "code", "message", "trace_id")
	if detail["code"] != wantCode {
		f.t.Fatalf("%s error code = %v, want %q (%s)", response.Request, detail["code"], wantCode, response.Body)
	}
}

// --- disposable database ----------------------------------------------------

func newFoundationDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate disposable database name: %v", err)
	}
	name := "vondel_compat_foundation_" + hex.EncodeToString(random[:])

	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse maintenance database URL: %v", err)
	}
	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse disposable database URL: %v", err)
	}
	testConfig.ConnConfig.Database = name

	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		adminPool.Close()
		t.Fatalf("create disposable database %q: %v", name, err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = adminPool.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		adminPool.Close()
		t.Fatalf("connect disposable database %q: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
		terminateCtx, cancelTerminate := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = adminPool.Exec(terminateCtx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		cancelTerminate()
		dropCtx, cancelDrop := context.WithTimeout(context.Background(), 30*time.Second)
		if _, err := adminPool.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %q: %v", name, err)
		}
		cancelDrop()
		adminPool.Close()
	})
	return pool
}

// --- companion enrollment helpers -------------------------------------------

type enrolledCompanion struct {
	Kind         string
	InstanceID   string
	Token        string
	Capabilities []string
}

func (f *foundationFixture) enrollmentSecret(kind string, capabilities []string) string {
	f.t.Helper()
	enrollment, err := f.admin.CreateEnrollment(f.ctx, kind, capabilities)
	if err != nil {
		f.t.Fatalf("mint %s enrollment: %v", kind, err)
	}
	if !strings.HasPrefix(enrollment.Secret, "vce_") {
		f.t.Fatalf("%s enrollment secret has the wrong family prefix", kind)
	}
	return enrollment.Secret
}

func foundationEnrollBody(secret, kind, instanceID string, minAPI, maxAPI int, capabilities []string) string {
	quoted := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		quoted = append(quoted, fmt.Sprintf("%q", capability))
	}
	return fmt.Sprintf(
		`{"secret":%q,"kind":%q,"instance_id":%q,"version":"1.0.0","image_digest":"sha256:%s","api":{"min":%d,"max":%d},"requested_capabilities":[%s]}`,
		secret, kind, instanceID, strings.Repeat("a", 64), minAPI, maxAPI, strings.Join(quoted, ","),
	)
}

func (f *foundationFixture) enroll(secret, kind, instanceID string, minAPI, maxAPI int, capabilities []string, idempotencyKey string) foundationResponse {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/internal/compat/v1/enroll",
		foundationEnrollBody(secret, kind, instanceID, minAPI, maxAPI, capabilities),
		map[string]string{"Idempotency-Key": idempotencyKey})
}

func (f *foundationFixture) applicationCount(kind, instanceID string) int {
	f.t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM compat_applications WHERE kind=$1 AND instance_id=$2`, kind, instanceID).Scan(&count); err != nil {
		f.t.Fatalf("count %s/%s applications: %v", kind, instanceID, err)
	}
	return count
}

func (f *foundationFixture) applicationRevision(instanceID string) int64 {
	f.t.Helper()
	applications, err := f.admin.ListApplications(f.ctx)
	if err != nil {
		f.t.Fatalf("list applications: %v", err)
	}
	for _, application := range applications {
		if application.InstanceID == instanceID {
			return application.Revision
		}
	}
	f.t.Fatalf("application %q is not enrolled", instanceID)
	return 0
}

// reportHealthy drives the companion's own health probe through the private
// API, which is what records liveness in the trust store and what the gateway
// reads back before it will route.
func (f *foundationFixture) reportHealthy(companion enrolledCompanion) {
	f.t.Helper()
	response := f.do(http.MethodGet, "/api/internal/compat/v1/health?status=healthy", "",
		map[string]string{"Authorization": "Bearer " + companion.Token})
	f.requireStatus(response, http.StatusOK)
}

// --- Task 7 acceptance: enrollment, API negotiation, idempotency -----------
//
// This is the first slice of TestCompatibilityFoundation. It exercises the
// fully real production path from an enrollment secret through
// internal/compatapp.Service to a stored compat_applications row: no test
// double sits between the HTTP request and the database here. Remaining
// behaviors in the plan's Step 1 list (login, PIN/device switching, routing,
// isolation, revocation, adult non-disclosure, cursor tampering, disabled
// routes, native health with both companions down) land in follow-up slices;
// several of them are blocked on production seams the file header already
// records as missing (no SubjectService/CatalogService adapter exists yet).
func TestCompatibilityFoundation(t *testing.T) {
	t.Run("Enroll_Success", func(t *testing.T) {
		f := newFoundationFixture(t)
		secret := f.enrollmentSecret("jellyfin", []string{"identity", "catalog"})

		response := f.requireStatus(
			f.enroll(secret, "jellyfin", "instance-1", 1, 1, []string{"identity", "catalog"}, "enroll-key-1"),
			http.StatusCreated,
		)

		credential := f.object(response)
		f.requireKeys("enroll response", credential, "application_id", "token", "expires_at", "granted_capabilities")
		if credential["application_id"] == "" {
			t.Fatal("enroll response application_id is empty")
		}
		if credential["token"] == "" {
			t.Fatal("enroll response token is empty")
		}
		if got := f.applicationCount("jellyfin", "instance-1"); got != 1 {
			t.Fatalf("applicationCount(jellyfin, instance-1) = %d, want 1", got)
		}
	})

	t.Run("Enroll_IncompatibleAPIVersion_Rejected", func(t *testing.T) {
		f := newFoundationFixture(t)
		secret := f.enrollmentSecret("jellyfin", []string{"identity"})

		response := f.enroll(secret, "jellyfin", "instance-incompatible", 2, 2, []string{"identity"}, "enroll-key-incompatible")

		f.requireErrorEnvelope(response, http.StatusConflict, "conflict")
		if got := f.applicationCount("jellyfin", "instance-incompatible"); got != 0 {
			t.Fatalf("applicationCount(jellyfin, instance-incompatible) = %d, want 0 after a rejected enrollment", got)
		}
	})

	t.Run("Enroll_IdempotentReplay_ReturnsStoredResponse", func(t *testing.T) {
		f := newFoundationFixture(t)
		secret := f.enrollmentSecret("audiobookshelf", []string{"identity", "catalog"})

		first := f.requireStatus(
			f.enroll(secret, "audiobookshelf", "instance-replay", 1, 1, []string{"identity", "catalog"}, "enroll-key-replay"),
			http.StatusCreated,
		)
		second := f.requireStatus(
			f.enroll(secret, "audiobookshelf", "instance-replay", 1, 1, []string{"identity", "catalog"}, "enroll-key-replay"),
			http.StatusCreated,
		)

		if second.Header.Get("Idempotency-Replayed") != "true" {
			t.Fatalf("replayed enroll response is missing Idempotency-Replayed: true (headers: %v)", second.Header)
		}
		if !bytes.Equal(first.Body, second.Body) {
			t.Fatalf("replayed enroll response body differs from the original:\nfirst:  %s\nsecond: %s", first.Body, second.Body)
		}
		// A one-use enrollment secret must still yield exactly one
		// application row: the replay is served from the idempotency store,
		// never by re-running enrollment against the (already consumed)
		// secret a second time.
		if got := f.applicationCount("audiobookshelf", "instance-replay"); got != 1 {
			t.Fatalf("applicationCount(audiobookshelf, instance-replay) = %d, want 1 after a replayed enroll", got)
		}
	})

	t.Run("Enroll_SameKeyDifferentBody_Conflicts", func(t *testing.T) {
		f := newFoundationFixture(t)
		// The enroll scope is derived from the secret digest (enrollment is
		// unauthenticated), so this must reuse one secret with a changed
		// body to land in the same idempotency scope.
		secret := f.enrollmentSecret("jellyfin", []string{"identity"})

		f.requireStatus(
			f.enroll(secret, "jellyfin", "instance-key-reuse-a", 1, 1, []string{"identity"}, "shared-key"),
			http.StatusCreated,
		)
		response := f.enroll(secret, "jellyfin", "instance-key-reuse-b", 1, 1, []string{"identity"}, "shared-key")

		f.requireErrorEnvelope(response, http.StatusConflict, "idempotency_conflict")
		if got := f.applicationCount("jellyfin", "instance-key-reuse-b"); got != 0 {
			t.Fatalf("applicationCount(jellyfin, instance-key-reuse-b) = %d, want 0: the conflicting body must never enroll", got)
		}
	})

	// --- routing, isolation, revocation ---------------------------------
	//
	// These drive the real compatgateway.Gateway composed exactly as
	// cmd/silo composes the public port (see newFoundationFixture), through
	// enrolled, healthy, admin-enabled applications backed by the real
	// compatapp.Service and the real admin adapter.

	t.Run("Routing_JellyfinPathReachesJellyfinCompanion", func(t *testing.T) {
		f := newFoundationFixture(t)
		f.enrollAndActivate("jellyfin", "jellyfin-routing", []string{"identity", "catalog"})

		response := f.requireStatus(f.do(http.MethodGet, "/System/Info", "", nil), http.StatusOK)

		seen := f.jellyfin.last(t)
		if seen.Method != http.MethodGet || seen.Path != "/System/Info" {
			t.Fatalf("jellyfin companion saw %s %s, want GET /System/Info", seen.Method, seen.Path)
		}
		if seen.Identity == "" {
			t.Fatal("forwarded request is missing the gateway-signed internal identity header")
		}
		if seen.Trace == "" {
			t.Fatal("forwarded request is missing the trace header")
		}
		if got := f.object(response)["companion"]; got != "jellyfin" {
			t.Fatalf("response body companion = %v, want jellyfin", got)
		}
		if f.audiobookshelf.count() != 0 {
			t.Fatalf("audiobookshelf companion received %d requests for a jellyfin-owned path, want 0", f.audiobookshelf.count())
		}
	})

	t.Run("Routing_AudiobookshelfPathStripsPrefixAndReachesItsCompanion", func(t *testing.T) {
		f := newFoundationFixture(t)
		f.enrollAndActivate("audiobookshelf", "abs-routing", []string{"identity", "catalog"})

		f.requireStatus(f.do(http.MethodGet, "/audiobookshelf/api/libraries", "", nil), http.StatusOK)

		seen := f.audiobookshelf.last(t)
		if seen.Path != "/api/libraries" {
			t.Fatalf("audiobookshelf companion saw path %q, want the /audiobookshelf prefix stripped to /api/libraries", seen.Path)
		}
		if f.jellyfin.count() != 0 {
			t.Fatalf("jellyfin companion received %d requests for an audiobookshelf-owned path, want 0", f.jellyfin.count())
		}
	})

	t.Run("Routing_NativeRouteNeverReachesEitherCompanion", func(t *testing.T) {
		f := newFoundationFixture(t)
		f.enrollAndActivate("jellyfin", "jellyfin-native-check", []string{"identity"})
		f.enrollAndActivate("audiobookshelf", "abs-native-check", []string{"identity"})

		response := f.requireStatus(f.do(http.MethodGet, "/", "", nil), http.StatusOK)

		if !strings.Contains(string(response.Body), "vondel") {
			t.Fatalf("GET / body = %s, want the native frontend fallback", response.Body)
		}
		if f.jellyfin.count() != 0 || f.audiobookshelf.count() != 0 {
			t.Fatalf("native route reached a companion: jellyfin=%d audiobookshelf=%d, want 0/0",
				f.jellyfin.count(), f.audiobookshelf.count())
		}
	})

	t.Run("Routing_RevokedApplicationIsUnavailableAndNeverForwarded", func(t *testing.T) {
		f := newFoundationFixture(t)
		f.enrollAndActivate("jellyfin", "jellyfin-revoked", []string{"identity"})

		revision := f.applicationRevision("jellyfin-revoked")
		if _, err := f.admin.RevokeApplication(f.ctx, "jellyfin-revoked", revision); err != nil {
			t.Fatalf("revoke jellyfin-revoked: %v", err)
		}

		response := f.do(http.MethodGet, "/System/Info", "", nil)

		f.requireGatewayError(response, http.StatusServiceUnavailable, "compatibility_unavailable")
		if f.jellyfin.count() != 0 {
			t.Fatalf("jellyfin companion received %d requests after revocation, want 0: a revoked application must never be forwarded to", f.jellyfin.count())
		}
	})

	t.Run("Routing_DisabledApplicationIsUnavailable", func(t *testing.T) {
		f := newFoundationFixture(t)
		f.enrollAndActivate("audiobookshelf", "abs-disabled", []string{"identity"})

		revision := f.applicationRevision("abs-disabled")
		if _, err := f.admin.SetApplicationEnabled(f.ctx, "abs-disabled", false, revision); err != nil {
			t.Fatalf("disable abs-disabled: %v", err)
		}

		response := f.do(http.MethodGet, "/audiobookshelf/api/libraries", "", nil)

		f.requireGatewayError(response, http.StatusServiceUnavailable, "compatibility_unavailable")
		if f.audiobookshelf.count() != 0 {
			t.Fatalf("audiobookshelf companion received %d requests while disabled, want 0", f.audiobookshelf.count())
		}
	})

	t.Run("Health_BothCompanionsStopped_NativeSurfaceStillAnswers", func(t *testing.T) {
		f := newFoundationFixture(t)
		f.enrollAndActivate("jellyfin", "jellyfin-both-down", []string{"identity"})
		f.enrollAndActivate("audiobookshelf", "abs-both-down", []string{"identity"})
		f.jellyfin.stop()
		f.audiobookshelf.stop()

		native := f.requireStatus(f.do(http.MethodGet, "/", "", nil), http.StatusOK)
		if !strings.Contains(string(native.Body), "vondel") {
			t.Fatalf("GET / body = %s, want the native frontend fallback even with both companions stopped", native.Body)
		}

		gatewayed := f.do(http.MethodGet, "/System/Info", "", nil)
		if gatewayed.Status < 500 {
			t.Fatalf("GET /System/Info with the companion stopped = %d, want a server-side failure status", gatewayed.Status)
		}
	})
}

// enrollAndActivate drives a companion through the full real lifecycle the
// gateway requires before it will route to it: enrollment, a healthy
// self-report through the private API, and an administrator enabling it
// through the real admin adapter. Every step is production code; only the
// two fake companions and the gateway's state provider are test doubles (see
// the file header).
func (f *foundationFixture) enrollAndActivate(kind, instanceID string, capabilities []string) enrolledCompanion {
	f.t.Helper()
	secret := f.enrollmentSecret(kind, capabilities)
	response := f.requireStatus(
		f.enroll(secret, kind, instanceID, 1, 1, capabilities, "enroll-"+instanceID),
		http.StatusCreated,
	)
	credential := f.object(response)
	token, _ := credential["token"].(string)
	if token == "" {
		f.t.Fatalf("enroll(%s, %s) returned no token", kind, instanceID)
	}
	companion := enrolledCompanion{Kind: kind, InstanceID: instanceID, Token: token, Capabilities: capabilities}
	f.reportHealthy(companion)

	revision := f.applicationRevision(instanceID)
	if _, err := f.admin.SetApplicationEnabled(f.ctx, instanceID, true, revision); err != nil {
		f.t.Fatalf("enable %s: %v", instanceID, err)
	}
	return companion
}

// requireGatewayError asserts the compatgateway error shape, which is
// deliberately not the compatapi error envelope: the gateway answers for
// companions that never got to speak Vondel's private API at all.
func (f *foundationFixture) requireGatewayError(response foundationResponse, wantStatus int, wantCode string) {
	f.t.Helper()
	f.requireStatus(response, wantStatus)
	var decoded struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body, &decoded); err != nil {
		f.t.Fatalf("%s gateway error body is not valid JSON: %v (%s)", response.Request, err, response.Body)
	}
	if decoded.Error != wantCode {
		f.t.Fatalf("%s gateway error = %q, want %q (%s)", response.Request, decoded.Error, wantCode, response.Body)
	}
}
