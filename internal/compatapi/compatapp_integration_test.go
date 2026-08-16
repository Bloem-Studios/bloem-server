package compatapi

// Integration tests wiring the REAL enrollment service (internal/compatapp)
// through CompatAppServices into the handler, against a disposable PostgreSQL
// database. The fake-backed handler suite proved the handler; this file
// proves the seam — vocabulary, error mapping, mTLS binding, renewal, image
// digest, kind declaration, and health reporting — end to end over HTTP.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/compatapp"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type integrationFixture struct {
	handler  *Handler
	service  *compatapp.Service
	store    *compatapp.Store
	subjects *fakeSubjects
	catalog  *fakeCatalog
}

// newIntegrationFixture builds a handler whose Apps/Enroller/Renewer/
// Heartbeats are the real compatapp.Service on a disposable database, with
// fakes only for the domain surfaces compatapp does not own.
func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		// Mirror internal/compatapp: with the require flag set, executing
		// nothing must fail loudly instead of reporting ok.
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newCompatAPIDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	service := compatapp.NewService(pool)

	subjects := &fakeSubjects{}
	subjects.loginDirect = func(email, password string, device DeviceClaim) (Subject, error) {
		if email == "reader@example.test" && password == "correct horse" {
			s := testSubject()
			s.Device = device
			return s, nil
		}
		return Subject{}, ErrInvalidCredentials
	}
	catalog := &fakeCatalog{libraries: []Library{{ID: "lib-1", Name: "Books", Kind: "audiobooks"}}}

	services := CompatAppServices(service)
	services.Subjects = subjects
	services.Catalog = catalog

	handler, err := New(Config{
		SubjectTokenKey: bytes.Repeat([]byte{0xC3}, 32),
		CursorKey:       bytes.Repeat([]byte{0xD4}, 32),
		Version:         "integration",
		APIRange:        APIRange{Min: compatapp.ServerAPIVersion, Max: compatapp.ServerAPIVersion},
	}, services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &integrationFixture{
		handler:  handler,
		service:  service,
		store:    compatapp.NewStore(pool),
		subjects: subjects,
		catalog:  catalog,
	}
}

func newCompatAPIDisposableDatabase(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate database name: %v", err)
	}
	name := "vondel_compatapi_" + hex.EncodeToString(random[:])

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
		_, _ = admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
	})
	return pool
}

// integrationPeerTLS mints a self-signed client certificate connection state,
// mirroring internal/compatapp's test helper (test code cannot be imported
// across packages).
func integrationPeerTLS(t *testing.T, commonName string) *tls.ConnectionState {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse test certificate: %v", err)
	}
	return &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
}

// withTLS attaches a peer TLS state to the outgoing request, standing in for
// a mutually authenticated connection.
func withTLS(state *tls.ConnectionState) reqOpt {
	return func(r *http.Request) { r.TLS = state }
}

// doIntegration mirrors fixture.do for the integration handler.
func (f *integrationFixture) do(t *testing.T, method, target string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	proxy := &fixture{handler: f.handler}
	return proxy.do(t, method, target, body, opts...)
}

// mustCreateEnrollment mints an admin enrollment through the real service
// using the canonical contract capability slugs.
func (f *integrationFixture) mustCreateEnrollment(t *testing.T, kind compatapp.Kind, capabilities ...string) compatapp.EnrollmentSecret {
	t.Helper()
	granted := make([]compatapp.Capability, 0, len(capabilities))
	for _, capability := range capabilities {
		granted = append(granted, compatapp.Capability(capability))
	}
	enrollment, err := f.service.CreateEnrollment(context.Background(), kind, granted)
	if err != nil {
		t.Fatalf("CreateEnrollment(%v): %v", capabilities, err)
	}
	return enrollment
}

func enrollBody(secret, kind, instanceID string, capabilities ...string) map[string]any {
	return map[string]any{
		"secret":                 secret,
		"kind":                   kind,
		"instance_id":            instanceID,
		"version":                "1.0.0",
		"api":                    map[string]int{"min": 1, "max": 1},
		"requested_capabilities": capabilities,
	}
}

// TestRealServiceEnrollLoginAndCapabilityGatedOp is the reviewer-demanded
// end-to-end proof: enroll through the real service, log a subject in, and
// exercise one capability-gated operation, all with the canonical contract
// vocabulary. It fails against the dotted enrollment vocabulary.
func TestRealServiceEnrollLoginAndCapabilityGatedOp(t *testing.T) {
	f := newIntegrationFixture(t)
	enrollment := f.mustCreateEnrollment(t, compatapp.KindAudiobookshelf, CapabilityIdentity, CapabilityCatalog)

	rec := f.do(t, http.MethodPost, "/enroll",
		enrollBody(enrollment.Secret, "audiobookshelf", "inst-e2e", CapabilityIdentity, CapabilityCatalog),
		withIdempotencyKey("e2e-enroll"))
	requireStatus(t, rec, http.StatusCreated)
	var credential struct {
		ApplicationID string   `json:"application_id"`
		Token         string   `json:"token"`
		Granted       []string `json:"granted_capabilities"`
	}
	decodeJSON(t, rec, &credential)
	if credential.Token == "" || credential.ApplicationID == "" {
		t.Fatalf("credential = %+v", credential)
	}

	// Login through the identity capability.
	rec = f.do(t, http.MethodPost, "/identity/login", map[string]any{
		"grant_type": "direct_profile",
		"email":      "reader@example.test",
		"password":   "correct horse",
		"device":     map[string]any{"id": "dev-e2e"},
	}, withBearer(credential.Token), withIdempotencyKey("e2e-login"))
	requireStatus(t, rec, http.StatusOK)
	var login struct {
		SubjectToken string `json:"subject_token"`
	}
	decodeJSON(t, rec, &login)

	// One capability-gated operation through the granted catalog capability.
	rec = f.do(t, http.MethodGet, "/catalog/libraries", nil, withBearer(credential.Token), withSubjectToken(login.SubjectToken))
	requireStatus(t, rec, http.StatusOK)

	// An ungranted capability still refuses.
	rec = f.do(t, http.MethodGet, "/events", nil, withBearer(credential.Token), withSubjectToken(login.SubjectToken))
	requireErrorCode(t, rec, http.StatusForbidden, "capability_missing")
}

// TestRealServiceErrorEnvelopes proves the adapter maps every relevant
// compatapp sentinel onto the stable envelope instead of collapsing to 500.
func TestRealServiceErrorEnvelopes(t *testing.T) {
	f := newIntegrationFixture(t)

	// Unknown enrollment secret: 401, not 500.
	rec := f.do(t, http.MethodPost, "/enroll",
		enrollBody("vce_definitely_wrong", "audiobookshelf", "inst-x", CapabilityCatalog),
		withIdempotencyKey("err-secret"))
	requireErrorCode(t, rec, http.StatusUnauthorized, "invalid_credentials")

	// Capability outside the reviewed grant: 400, and the secret survives.
	enrollment := f.mustCreateEnrollment(t, compatapp.KindAudiobookshelf, CapabilityCatalog)
	rec = f.do(t, http.MethodPost, "/enroll",
		enrollBody(enrollment.Secret, "audiobookshelf", "inst-grant", CapabilityCatalog, CapabilityState),
		withIdempotencyKey("err-grant"))
	requireErrorCode(t, rec, http.StatusBadRequest, "invalid_request")

	// Incompatible API range: 409, and the secret still survives.
	body := enrollBody(enrollment.Secret, "audiobookshelf", "inst-range", CapabilityCatalog)
	body["api"] = map[string]int{"min": 99, "max": 99}
	rec = f.do(t, http.MethodPost, "/enroll", body, withIdempotencyKey("err-range"))
	requireErrorCode(t, rec, http.StatusConflict, "conflict")

	// The un-consumed secret now enrolls cleanly.
	rec = f.do(t, http.MethodPost, "/enroll",
		enrollBody(enrollment.Secret, "audiobookshelf", "inst-ok", CapabilityCatalog),
		withIdempotencyKey("err-ok"))
	requireStatus(t, rec, http.StatusCreated)

	// The same instance enrolling again is a conflict, not a 500.
	second := f.mustCreateEnrollment(t, compatapp.KindAudiobookshelf, CapabilityCatalog)
	rec = f.do(t, http.MethodPost, "/enroll",
		enrollBody(second.Secret, "audiobookshelf", "inst-ok", CapabilityCatalog),
		withIdempotencyKey("err-dup"))
	requireErrorCode(t, rec, http.StatusConflict, "conflict")
}

// TestRealServiceMTLSBindingAndRenewal proves the enrollment connection's
// client certificate is bound to the application and that renewal reuses the
// middleware-authenticated identity instead of re-authenticating with a nil
// connection state.
func TestRealServiceMTLSBindingAndRenewal(t *testing.T) {
	f := newIntegrationFixture(t)
	peer := integrationPeerTLS(t, "vondel-audiobookshelf")
	enrollment := f.mustCreateEnrollment(t, compatapp.KindAudiobookshelf, CapabilityCatalog)

	rec := f.do(t, http.MethodPost, "/enroll",
		enrollBody(enrollment.Secret, "audiobookshelf", "inst-mtls", CapabilityCatalog),
		withIdempotencyKey("mtls-enroll"), withTLS(peer))
	requireStatus(t, rec, http.StatusCreated)
	var credential struct {
		ApplicationID string `json:"application_id"`
		Token         string `json:"token"`
	}
	decodeJSON(t, rec, &credential)

	application, err := f.store.Application(context.Background(), credential.ApplicationID)
	if err != nil {
		t.Fatalf("Application: %v", err)
	}
	if application.TLSFingerprint == "" {
		t.Fatal("the enrollment connection's client certificate must be bound to the application")
	}

	// The bound certificate is required from now on.
	rec = f.do(t, http.MethodGet, "/health", nil, withBearer(credential.Token))
	requireErrorCode(t, rec, http.StatusUnauthorized, "unauthorized")
	rec = f.do(t, http.MethodGet, "/health", nil, withBearer(credential.Token), withTLS(peer))
	requireStatus(t, rec, http.StatusOK)

	// Renewal must work for an mTLS-bound application: the middleware already
	// authenticated the bearer with the connection state.
	rec = f.do(t, http.MethodPost, "/credentials/renew", nil,
		withBearer(credential.Token), withTLS(peer), withIdempotencyKey("mtls-renew"))
	requireStatus(t, rec, http.StatusOK)
	var renewed struct {
		Token string `json:"token"`
	}
	decodeJSON(t, rec, &renewed)
	if renewed.Token == "" || renewed.Token == credential.Token {
		t.Fatalf("renewal must issue a fresh credential, got %q", renewed.Token)
	}

	// Rotation revoked the old credential; only the new one authenticates.
	rec = f.do(t, http.MethodGet, "/health", nil, withBearer(credential.Token), withTLS(peer))
	requireErrorCode(t, rec, http.StatusUnauthorized, "unauthorized")
	rec = f.do(t, http.MethodGet, "/health", nil, withBearer(renewed.Token), withTLS(peer))
	requireStatus(t, rec, http.StatusOK)
}

// TestRealServiceImageDigestAndKindDeclaration proves the enrollment image
// digest reaches the trust record and that a kind declaration contradicting
// the enrollment secret is refused without consuming the secret.
func TestRealServiceImageDigestAndKindDeclaration(t *testing.T) {
	f := newIntegrationFixture(t)
	enrollment := f.mustCreateEnrollment(t, compatapp.KindAudiobookshelf, CapabilityCatalog)

	// A contradicting kind declaration is a conflict.
	rec := f.do(t, http.MethodPost, "/enroll",
		enrollBody(enrollment.Secret, "jellyfin", "inst-digest", CapabilityCatalog),
		withIdempotencyKey("kind-wrong"))
	requireErrorCode(t, rec, http.StatusConflict, "conflict")

	// The secret was not consumed: the honest declaration still enrolls, and
	// the image digest lands on the durable application record.
	body := enrollBody(enrollment.Secret, "audiobookshelf", "inst-digest", CapabilityCatalog)
	body["image_digest"] = "sha256:feedfacecafebeef"
	rec = f.do(t, http.MethodPost, "/enroll", body, withIdempotencyKey("kind-right"))
	requireStatus(t, rec, http.StatusCreated)
	var credential struct {
		ApplicationID string `json:"application_id"`
	}
	decodeJSON(t, rec, &credential)

	application, err := f.store.Application(context.Background(), credential.ApplicationID)
	if err != nil {
		t.Fatalf("Application: %v", err)
	}
	if application.ImageDigest != "sha256:feedfacecafebeef" {
		t.Fatalf("image digest = %q, want sha256:feedfacecafebeef", application.ImageDigest)
	}
	if application.Kind != compatapp.KindAudiobookshelf {
		t.Fatalf("kind = %q", application.Kind)
	}
}

// TestRealServiceHealthReporting proves /health records the reported status
// (unknown when absent) and refuses statuses outside the vocabulary.
func TestRealServiceHealthReporting(t *testing.T) {
	f := newIntegrationFixture(t)
	enrollment := f.mustCreateEnrollment(t, compatapp.KindJellyfin, CapabilityCatalog)
	rec := f.do(t, http.MethodPost, "/enroll",
		enrollBody(enrollment.Secret, "jellyfin", "inst-health", CapabilityCatalog),
		withIdempotencyKey("health-enroll"))
	requireStatus(t, rec, http.StatusCreated)
	var credential struct {
		ApplicationID string `json:"application_id"`
		Token         string `json:"token"`
	}
	decodeJSON(t, rec, &credential)

	readHealth := func() compatapp.Application {
		t.Helper()
		application, err := f.store.Application(context.Background(), credential.ApplicationID)
		if err != nil {
			t.Fatalf("Application: %v", err)
		}
		return application
	}

	// A probe without a status is contact-only: recorded as unknown.
	rec = f.do(t, http.MethodGet, "/health", nil, withBearer(credential.Token))
	requireStatus(t, rec, http.StatusOK)
	if got := readHealth(); got.Health != compatapp.HealthUnknown || got.LastContactAt == nil {
		t.Fatalf("statusless probe recorded health=%q contact=%v", got.Health, got.LastContactAt)
	}

	// A reported status is recorded verbatim.
	rec = f.do(t, http.MethodGet, "/health?status=degraded", nil, withBearer(credential.Token))
	requireStatus(t, rec, http.StatusOK)
	if got := readHealth(); got.Health != compatapp.HealthDegraded {
		t.Fatalf("reported status recorded health=%q, want degraded", got.Health)
	}

	// A status outside the vocabulary is refused before anything is recorded.
	rec = f.do(t, http.MethodGet, "/health?status=on-fire", nil, withBearer(credential.Token))
	requireErrorCode(t, rec, http.StatusBadRequest, "invalid_request")
	if got := readHealth(); got.Health != compatapp.HealthDegraded {
		t.Fatalf("refused status must not overwrite health, got %q", got.Health)
	}
}
