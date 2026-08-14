package compatapp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// unknownApplicationID is a syntactically valid UUID that no test creates.
const unknownApplicationID = "00000000-0000-0000-0000-000000000001"

func newCompatAppService(t *testing.T) *Service {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		// The plan's verification gates run this package with the require
		// flag set; skipping silently there would let "go test" report ok
		// having executed nothing. Same convention as the tenant identity
		// migration tests.
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newCompatAppDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return NewService(pool)
}

// setServiceClock freezes the service's clock at a fixed instant. Expiry
// predicates compare stored timestamps against the injected clock, so tests
// can cross the 15-minute boundaries without sleeping.
func setServiceClock(s *Service, at time.Time) {
	s.now = func() time.Time { return at }
}

func newTestPeerTLS(t *testing.T, commonName string) *tls.ConnectionState {
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

func validEnrollmentRequest(instanceID string) EnrollmentRequest {
	return EnrollmentRequest{
		InstanceID:   instanceID,
		Version:      "1.2.3",
		ImageDigest:  "sha256:0123456789abcdef",
		APIRangeMin:  1,
		APIRangeMax:  1,
		Capabilities: []Capability{CapabilityCatalog, CapabilityPlayback},
	}
}

func mustCreateEnrollment(t *testing.T, s *Service, kind Kind, capabilities ...Capability) EnrollmentSecret {
	t.Helper()
	if len(capabilities) == 0 {
		capabilities = []Capability{
			CapabilityIdentity,
			CapabilityCatalog,
			CapabilityPlayback,
			CapabilityState,
		}
	}
	enrollment, err := s.CreateEnrollment(context.Background(), kind, capabilities)
	if err != nil {
		t.Fatalf("CreateEnrollment: %v", err)
	}
	return enrollment
}

func mustEnroll(t *testing.T, s *Service, instanceID string) ServiceCredential {
	t.Helper()
	enrollment := mustCreateEnrollment(t, s, KindJellyfin)
	credential, err := s.Enroll(context.Background(), enrollment.Secret, validEnrollmentRequest(instanceID))
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return credential
}

func auditEventNames(t *testing.T, s *Service, applicationID string) []string {
	t.Helper()
	events, err := s.store.AuditEvents(context.Background(), applicationID)
	if err != nil {
		t.Fatalf("AuditEvents: %v", err)
	}
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Event)
	}
	return names
}

func countAuditEvents(names []string, want string) int {
	count := 0
	for _, name := range names {
		if name == want {
			count++
		}
	}
	return count
}

func TestCreateEnrollmentFailsClosedOnUnknownKindAndCapabilities(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)

	if _, err := service.CreateEnrollment(ctx, Kind("plex"), []Capability{CapabilityCatalog}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("CreateEnrollment unknown kind error = %v, want ErrUnknownKind", err)
	}
	if _, err := service.CreateEnrollment(ctx, KindJellyfin, []Capability{Capability("catalog.everything")}); !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("CreateEnrollment unknown capability error = %v, want ErrUnknownCapability", err)
	}
	// The retired dotted vocabulary must stay retired: only the contract's
	// slugs enroll.
	if _, err := service.CreateEnrollment(ctx, KindJellyfin, []Capability{Capability("catalog.read")}); !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("CreateEnrollment dotted-name error = %v, want ErrUnknownCapability", err)
	}
	// The contract's implicit "service" capability is never grantable.
	if _, err := service.CreateEnrollment(ctx, KindJellyfin, []Capability{Capability("service")}); !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("CreateEnrollment service-capability error = %v, want ErrUnknownCapability", err)
	}
	if _, err := service.CreateEnrollment(ctx, KindJellyfin, nil); !errors.Is(err, ErrNoCapabilities) {
		t.Fatalf("CreateEnrollment empty grant error = %v, want ErrNoCapabilities", err)
	}
}

func TestEnrollRefusesContradictingKindDeclaration(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	enrollment := mustCreateEnrollment(t, service, KindAudiobookshelf, CapabilityCatalog)

	request := validEnrollmentRequest("inst-declared-kind")
	request.Capabilities = []Capability{CapabilityCatalog}
	request.DeclaredKind = KindJellyfin
	if _, err := service.Enroll(ctx, enrollment.Secret, request); !errors.Is(err, ErrKindMismatch) {
		t.Fatalf("Enroll contradicting kind error = %v, want ErrKindMismatch", err)
	}

	// The mismatch must not consume the secret: the honest declaration
	// enrolls with the same enrollment.
	request.DeclaredKind = KindAudiobookshelf
	credential, err := service.Enroll(ctx, enrollment.Secret, request)
	if err != nil {
		t.Fatalf("Enroll after corrected declaration: %v", err)
	}
	identity, err := service.Authenticate(ctx, credential.Secret, nil)
	if err != nil || identity.Kind != KindAudiobookshelf {
		t.Fatalf("identity = %+v err = %v, want audiobookshelf", identity, err)
	}
}

func TestCreateEnrollmentReturnsSecretOnceAndStoresOnlyDigest(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	issued := time.Now().UTC().Truncate(time.Second)
	setServiceClock(service, issued)

	enrollment, err := service.CreateEnrollment(ctx, KindAudiobookshelf, []Capability{CapabilityCatalog})
	if err != nil {
		t.Fatalf("CreateEnrollment: %v", err)
	}
	if !strings.HasPrefix(enrollment.Secret, "vce_") || len(enrollment.Secret) < 24 {
		t.Fatalf("enrollment secret = %q, want long vce_-prefixed secret", enrollment.Secret)
	}
	if enrollment.Kind != KindAudiobookshelf || enrollment.ID == "" {
		t.Fatalf("enrollment = %#v, want identified audiobookshelf enrollment", enrollment)
	}
	if !enrollment.ExpiresAt.Equal(issued.Add(EnrollmentTTL)) {
		t.Fatalf("enrollment expiry = %v, want %v", enrollment.ExpiresAt, issued.Add(EnrollmentTTL))
	}

	var storedDigest string
	if err := service.store.pool.QueryRow(ctx, `
		SELECT secret_digest FROM compat_application_enrollments WHERE id = $1`,
		enrollment.ID).Scan(&storedDigest); err != nil {
		t.Fatalf("load stored enrollment: %v", err)
	}
	rawDigest := sha256.Sum256([]byte(enrollment.Secret))
	if storedDigest != hex.EncodeToString(rawDigest[:]) {
		t.Fatalf("stored digest = %q, want SHA-256 of the issued secret", storedDigest)
	}
	if storedDigest == enrollment.Secret || strings.Contains(storedDigest, enrollment.Secret) {
		t.Fatal("raw enrollment secret is stored at rest")
	}

	var auditCount int
	if err := service.store.pool.QueryRow(ctx, `
		SELECT count(*) FROM compat_application_audit
		WHERE enrollment_id = $1 AND event = 'enrollment_created'`,
		enrollment.ID).Scan(&auditCount); err != nil {
		t.Fatalf("load enrollment audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("enrollment_created audit rows = %d, want 1", auditCount)
	}
}

func TestEnrollIssuesHashedCredentialAndConsumesSecret(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	issued := time.Now().UTC().Truncate(time.Second)
	setServiceClock(service, issued)

	enrollment := mustCreateEnrollment(t, service, KindJellyfin)
	request := validEnrollmentRequest("jellyfin-main")
	credential, err := service.Enroll(ctx, enrollment.Secret, request)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if !strings.HasPrefix(credential.Secret, "vcc_") || len(credential.Secret) < 24 {
		t.Fatalf("service credential = %q, want long vcc_-prefixed secret", credential.Secret)
	}
	if credential.ApplicationID == "" || credential.CredentialID == "" {
		t.Fatalf("credential = %#v, want identified credential", credential)
	}
	if !credential.ExpiresAt.Equal(issued.Add(CredentialTTL)) {
		t.Fatalf("credential expiry = %v, want %v", credential.ExpiresAt, issued.Add(CredentialTTL))
	}

	application, err := service.store.Application(ctx, credential.ApplicationID)
	if err != nil {
		t.Fatalf("Application: %v", err)
	}
	if application.Kind != KindJellyfin || application.InstanceID != "jellyfin-main" {
		t.Fatalf("application = %#v, want enrolled jellyfin-main", application)
	}
	if application.Version != request.Version || application.ImageDigest != request.ImageDigest {
		t.Fatalf("application = %#v, want recorded version and image digest", application)
	}
	if application.APIRangeMin != 1 || application.APIRangeMax != 1 {
		t.Fatalf("application API range = [%d,%d], want [1,1]", application.APIRangeMin, application.APIRangeMax)
	}
	if !application.Enabled || application.RevokedAt != nil || application.TLSFingerprint != "" {
		t.Fatalf("application = %#v, want enabled unrevoked same-host application", application)
	}
	wantCapabilities := []Capability{CapabilityCatalog, CapabilityPlayback}
	if len(application.Capabilities) != len(wantCapabilities) {
		t.Fatalf("granted capabilities = %v, want %v", application.Capabilities, wantCapabilities)
	}
	for i, capability := range wantCapabilities {
		if application.Capabilities[i] != capability {
			t.Fatalf("granted capabilities = %v, want %v", application.Capabilities, wantCapabilities)
		}
	}

	var storedDigest string
	if err := service.store.pool.QueryRow(ctx, `
		SELECT secret_digest FROM compat_application_credentials WHERE id = $1`,
		credential.CredentialID).Scan(&storedDigest); err != nil {
		t.Fatalf("load stored credential: %v", err)
	}
	rawDigest := sha256.Sum256([]byte(credential.Secret))
	if storedDigest != hex.EncodeToString(rawDigest[:]) {
		t.Fatalf("stored credential digest = %q, want SHA-256 of the issued secret", storedDigest)
	}

	// The secret is single-use: the winning enrollment consumed it.
	if _, err := service.Enroll(ctx, enrollment.Secret, validEnrollmentRequest("jellyfin-second")); !errors.Is(err, ErrEnrollmentDenied) {
		t.Fatalf("Enroll reused secret error = %v, want ErrEnrollmentDenied", err)
	}

	names := auditEventNames(t, service, credential.ApplicationID)
	if countAuditEvents(names, "enrolled") != 1 {
		t.Fatalf("audit events = %v, want exactly one enrolled row", names)
	}
}

func TestEnrollRejectsExpiredSecret(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	issued := time.Now().UTC()
	setServiceClock(service, issued)
	enrollment := mustCreateEnrollment(t, service, KindJellyfin)

	setServiceClock(service, issued.Add(EnrollmentTTL+time.Minute))
	if _, err := service.Enroll(ctx, enrollment.Secret, validEnrollmentRequest("late")); !errors.Is(err, ErrEnrollmentDenied) {
		t.Fatalf("Enroll expired secret error = %v, want ErrEnrollmentDenied", err)
	}
}

func TestEnrollRejectsUnknownOrForeignSecrets(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)

	for _, secret := range []string{
		"",
		"not-a-secret",
		"vce_definitely-not-issued-by-this-server",
		"vcc_definitely-not-an-enrollment-secret",
	} {
		if _, err := service.Enroll(ctx, secret, validEnrollmentRequest("nope")); !errors.Is(err, ErrEnrollmentDenied) {
			t.Fatalf("Enroll(%q) error = %v, want ErrEnrollmentDenied", secret, err)
		}
	}
}

func TestEnrollEnforcesCapabilitySubsetWithoutBurningTheSecret(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	enrollment := mustCreateEnrollment(t, service, KindJellyfin, CapabilityCatalog)

	request := validEnrollmentRequest("subset")
	request.Capabilities = []Capability{CapabilityCatalog, CapabilityState}
	if _, err := service.Enroll(ctx, enrollment.Secret, request); !errors.Is(err, ErrCapabilityNotGranted) {
		t.Fatalf("Enroll over-grant error = %v, want ErrCapabilityNotGranted", err)
	}

	request.Capabilities = []Capability{Capability("filesystem.read")}
	if _, err := service.Enroll(ctx, enrollment.Secret, request); !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("Enroll unknown capability error = %v, want ErrUnknownCapability", err)
	}

	request.Capabilities = nil
	if _, err := service.Enroll(ctx, enrollment.Secret, request); !errors.Is(err, ErrNoCapabilities) {
		t.Fatalf("Enroll empty request error = %v, want ErrNoCapabilities", err)
	}

	// A rejected request must not consume the admin's secret: the corrected
	// companion configuration still enrolls.
	request.Capabilities = []Capability{CapabilityCatalog}
	if _, err := service.Enroll(ctx, enrollment.Secret, request); err != nil {
		t.Fatalf("Enroll corrected request: %v", err)
	}
}

func TestEnrollFailsClosedOnAPIRangeAndRequestShape(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)

	cases := []struct {
		name    string
		mutate  func(*EnrollmentRequest)
		wantErr error
	}{
		{"range above server", func(r *EnrollmentRequest) { r.APIRangeMin = 2; r.APIRangeMax = 3 }, ErrAPIRangeUnsupported},
		{"range below server", func(r *EnrollmentRequest) { r.APIRangeMin = 0; r.APIRangeMax = 0 }, ErrInvalidEnrollmentRequest},
		{"inverted range", func(r *EnrollmentRequest) { r.APIRangeMin = 2; r.APIRangeMax = 1 }, ErrInvalidEnrollmentRequest},
		{"blank instance", func(r *EnrollmentRequest) { r.InstanceID = "   " }, ErrInvalidEnrollmentRequest},
		{"blank version", func(r *EnrollmentRequest) { r.Version = "" }, ErrInvalidEnrollmentRequest},
	}
	for _, testCase := range cases {
		enrollment := mustCreateEnrollment(t, service, KindJellyfin)
		request := validEnrollmentRequest("shape-" + testCase.name)
		testCase.mutate(&request)
		if _, err := service.Enroll(ctx, enrollment.Secret, request); !errors.Is(err, testCase.wantErr) {
			t.Fatalf("Enroll %s error = %v, want %v", testCase.name, err, testCase.wantErr)
		}
	}
}

func TestEnrollRejectsDuplicateInstanceIdentity(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	mustEnroll(t, service, "shared-instance")

	enrollment := mustCreateEnrollment(t, service, KindJellyfin)
	if _, err := service.Enroll(ctx, enrollment.Secret, validEnrollmentRequest("shared-instance")); !errors.Is(err, ErrInstanceAlreadyEnrolled) {
		t.Fatalf("Enroll duplicate instance error = %v, want ErrInstanceAlreadyEnrolled", err)
	}

	// The same instance identifier under the other kind is a different
	// application instance.
	otherKind, err := service.CreateEnrollment(ctx, KindAudiobookshelf, []Capability{CapabilityCatalog})
	if err != nil {
		t.Fatalf("CreateEnrollment: %v", err)
	}
	request := validEnrollmentRequest("shared-instance")
	request.Capabilities = []Capability{CapabilityCatalog}
	if _, err := service.Enroll(ctx, otherKind.Secret, request); err != nil {
		t.Fatalf("Enroll same instance under other kind: %v", err)
	}
}

func TestConcurrentEnrollmentReplayAdmitsExactlyOne(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	enrollment := mustCreateEnrollment(t, service, KindJellyfin)

	const attempts = 8
	var wg sync.WaitGroup
	results := make([]error, attempts)
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			<-start
			request := validEnrollmentRequest("replay-" + string(rune('a'+slot)))
			_, err := service.Enroll(ctx, enrollment.Secret, request)
			results[slot] = err
		}(i)
	}
	close(start)
	wg.Wait()

	admitted := 0
	for slot, err := range results {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, ErrEnrollmentDenied):
		default:
			t.Fatalf("concurrent Enroll slot %d error = %v, want nil or ErrEnrollmentDenied", slot, err)
		}
	}
	if admitted != 1 {
		t.Fatalf("concurrent enrollment admitted %d attempts, want exactly 1", admitted)
	}
}

func TestAuthenticateReturnsIdentityAndRenewsCredential(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	issued := time.Now().UTC()
	setServiceClock(service, issued)
	credential := mustEnroll(t, service, "renewing")

	identity, err := service.Authenticate(ctx, credential.Secret, nil)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if identity.ApplicationID != credential.ApplicationID || identity.Kind != KindJellyfin || identity.InstanceID != "renewing" {
		t.Fatalf("identity = %#v, want the enrolled application", identity)
	}
	if len(identity.Capabilities) == 0 {
		t.Fatalf("identity = %#v, want granted capabilities", identity)
	}

	// Renewal slides the 15-minute window forward on every successful use.
	setServiceClock(service, issued.Add(10*time.Minute))
	if _, err := service.Authenticate(ctx, credential.Secret, nil); err != nil {
		t.Fatalf("Authenticate at +10m: %v", err)
	}
	setServiceClock(service, issued.Add(20*time.Minute))
	if _, err := service.Authenticate(ctx, credential.Secret, nil); err != nil {
		t.Fatalf("Authenticate at +20m after renewal: %v", err)
	}

	// An idle credential expires 15 minutes after its last use.
	setServiceClock(service, issued.Add(20*time.Minute).Add(CredentialTTL+time.Minute))
	if _, err := service.Authenticate(ctx, credential.Secret, nil); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("Authenticate expired credential error = %v, want ErrCredentialInvalid", err)
	}
}

func TestAuthenticateRefusesUnknownSecrets(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	enrollment := mustCreateEnrollment(t, service, KindJellyfin)

	for _, bearer := range []string{
		"",
		"vcc_never-issued-by-this-server",
		"vce_prefix-of-the-wrong-secret-family",
		enrollment.Secret,
	} {
		if _, err := service.Authenticate(ctx, bearer, nil); !errors.Is(err, ErrCredentialInvalid) {
			t.Fatalf("Authenticate(%q) error = %v, want ErrCredentialInvalid", bearer, err)
		}
	}
}

func TestAuthenticateRefusesDisabledAndRevokedApplications(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "lifecycle")
	applicationID := credential.ApplicationID

	if err := service.SetEnabled(ctx, applicationID, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if _, err := service.Authenticate(ctx, credential.Secret, nil); !errors.Is(err, ErrApplicationDisabled) {
		t.Fatalf("Authenticate disabled application error = %v, want ErrApplicationDisabled", err)
	}
	if err := service.SetEnabled(ctx, applicationID, true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if _, err := service.Authenticate(ctx, credential.Secret, nil); err != nil {
		t.Fatalf("Authenticate re-enabled application: %v", err)
	}

	if err := service.Revoke(ctx, applicationID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := service.Authenticate(ctx, credential.Secret, nil); !errors.Is(err, ErrApplicationRevoked) {
		t.Fatalf("Authenticate revoked application error = %v, want ErrApplicationRevoked", err)
	}
	if _, err := service.Rotate(ctx, applicationID); !errors.Is(err, ErrApplicationRevoked) {
		t.Fatalf("Rotate revoked application error = %v, want ErrApplicationRevoked", err)
	}
	if err := service.SetEnabled(ctx, applicationID, true); !errors.Is(err, ErrApplicationRevoked) {
		t.Fatalf("SetEnabled revoked application error = %v, want ErrApplicationRevoked", err)
	}
	// Revocation is idempotent and audited once.
	if err := service.Revoke(ctx, applicationID); err != nil {
		t.Fatalf("Revoke twice: %v", err)
	}

	names := auditEventNames(t, service, applicationID)
	for event, want := range map[string]int{"enrolled": 1, "disabled": 1, "enabled": 1, "revoked": 1} {
		if got := countAuditEvents(names, event); got != want {
			t.Fatalf("audit events = %v, want %d %q rows", names, want, event)
		}
	}
}

func TestAuthenticateRefusesStaleAPIRange(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "stale-range")

	// Simulate a server whose supported version moved past the range the
	// companion registered at enrollment.
	if _, err := service.store.pool.Exec(ctx, `
		UPDATE compat_applications SET api_range_min = $2, api_range_max = $3 WHERE id = $1`,
		credential.ApplicationID, ServerAPIVersion+1, ServerAPIVersion+2); err != nil {
		t.Fatalf("record stale API range: %v", err)
	}
	if _, err := service.Authenticate(ctx, credential.Secret, nil); !errors.Is(err, ErrAPIRangeUnsupported) {
		t.Fatalf("Authenticate stale API range error = %v, want ErrAPIRangeUnsupported", err)
	}
}

func TestRotateAndRevokeAreIndependentPerApplication(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	first := mustEnroll(t, service, "first-app")
	second := mustEnroll(t, service, "second-app")

	rotated, err := service.Rotate(ctx, first.ApplicationID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.Secret == first.Secret || !strings.HasPrefix(rotated.Secret, "vcc_") {
		t.Fatalf("rotated secret = %q, want a fresh vcc_ credential", rotated.Secret)
	}
	if _, err := service.Authenticate(ctx, first.Secret, nil); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("Authenticate pre-rotation secret error = %v, want ErrCredentialInvalid", err)
	}
	if _, err := service.Authenticate(ctx, rotated.Secret, nil); err != nil {
		t.Fatalf("Authenticate rotated secret: %v", err)
	}
	// Rotating the first application leaves the second untouched.
	if _, err := service.Authenticate(ctx, second.Secret, nil); err != nil {
		t.Fatalf("Authenticate second application after first rotation: %v", err)
	}

	if err := service.Revoke(ctx, second.ApplicationID); err != nil {
		t.Fatalf("Revoke second: %v", err)
	}
	if _, err := service.Authenticate(ctx, second.Secret, nil); !errors.Is(err, ErrApplicationRevoked) {
		t.Fatalf("Authenticate revoked second error = %v, want ErrApplicationRevoked", err)
	}
	if _, err := service.Authenticate(ctx, rotated.Secret, nil); err != nil {
		t.Fatalf("Authenticate first application after second revocation: %v", err)
	}

	if got := countAuditEvents(auditEventNames(t, service, first.ApplicationID), "rotated"); got != 1 {
		t.Fatalf("rotated audit rows = %d, want 1", got)
	}
}

func TestAuthenticateBindsEnrolledPeerCertificate(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	boundPeer := newTestPeerTLS(t, "vondel-jellyfin")
	otherPeer := newTestPeerTLS(t, "impostor")

	enrollment := mustCreateEnrollment(t, service, KindJellyfin)
	request := validEnrollmentRequest("remote-mtls")
	request.PeerTLS = boundPeer
	credential, err := service.Enroll(ctx, enrollment.Secret, request)
	if err != nil {
		t.Fatalf("Enroll with peer certificate: %v", err)
	}

	application, err := service.store.Application(ctx, credential.ApplicationID)
	if err != nil {
		t.Fatalf("Application: %v", err)
	}
	fingerprint := sha256.Sum256(boundPeer.PeerCertificates[0].Raw)
	if application.TLSFingerprint != hex.EncodeToString(fingerprint[:]) {
		t.Fatalf("bound fingerprint = %q, want SHA-256 of the enrolled leaf certificate", application.TLSFingerprint)
	}

	if _, err := service.Authenticate(ctx, credential.Secret, nil); !errors.Is(err, ErrPeerCertificateRequired) {
		t.Fatalf("Authenticate without peer certificate error = %v, want ErrPeerCertificateRequired", err)
	}
	if _, err := service.Authenticate(ctx, credential.Secret, otherPeer); !errors.Is(err, ErrPeerCertificateMismatch) {
		t.Fatalf("Authenticate with foreign certificate error = %v, want ErrPeerCertificateMismatch", err)
	}
	if _, err := service.Authenticate(ctx, credential.Secret, boundPeer); err != nil {
		t.Fatalf("Authenticate with bound certificate: %v", err)
	}

	// A same-host application with no bound certificate authenticates by
	// credential alone, with or without an incidental client certificate.
	sameHost := mustEnroll(t, service, "same-host")
	if _, err := service.Authenticate(ctx, sameHost.Secret, nil); err != nil {
		t.Fatalf("Authenticate same-host without TLS: %v", err)
	}
	if _, err := service.Authenticate(ctx, sameHost.Secret, otherPeer); err != nil {
		t.Fatalf("Authenticate same-host with incidental certificate: %v", err)
	}
}

func TestSetEnabledNoOpDoesNotDuplicateAudit(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	credential := mustEnroll(t, service, "noop-enable")

	if err := service.SetEnabled(ctx, credential.ApplicationID, true); err != nil {
		t.Fatalf("SetEnabled no-op: %v", err)
	}
	names := auditEventNames(t, service, credential.ApplicationID)
	if got := countAuditEvents(names, "enabled"); got != 0 {
		t.Fatalf("enabled audit rows after no-op = %d, want 0", got)
	}
}

func TestHeartbeatRecordsHealthAndLastContact(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)
	at := time.Now().UTC().Truncate(time.Second)
	setServiceClock(service, at)
	credential := mustEnroll(t, service, "heartbeating")

	beat := at.Add(5 * time.Minute)
	setServiceClock(service, beat)
	if err := service.Heartbeat(ctx, credential.ApplicationID, HealthHealthy); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	application, err := service.store.Application(ctx, credential.ApplicationID)
	if err != nil {
		t.Fatalf("Application: %v", err)
	}
	if application.Health != HealthHealthy {
		t.Fatalf("health = %q, want %q", application.Health, HealthHealthy)
	}
	if application.LastContactAt == nil || !application.LastContactAt.Equal(beat) {
		t.Fatalf("last contact = %v, want %v", application.LastContactAt, beat)
	}

	if err := service.Heartbeat(ctx, credential.ApplicationID, HealthStatus("thriving")); !errors.Is(err, ErrInvalidHealthStatus) {
		t.Fatalf("Heartbeat invalid status error = %v, want ErrInvalidHealthStatus", err)
	}
	if err := service.Heartbeat(ctx, unknownApplicationID, HealthHealthy); !errors.Is(err, ErrApplicationNotFound) {
		t.Fatalf("Heartbeat unknown application error = %v, want ErrApplicationNotFound", err)
	}
	if err := service.Revoke(ctx, credential.ApplicationID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := service.Heartbeat(ctx, credential.ApplicationID, HealthHealthy); !errors.Is(err, ErrApplicationRevoked) {
		t.Fatalf("Heartbeat revoked application error = %v, want ErrApplicationRevoked", err)
	}
}

func TestLifecycleOperationsRequireAKnownApplication(t *testing.T) {
	ctx := context.Background()
	service := newCompatAppService(t)

	if _, err := service.Rotate(ctx, unknownApplicationID); !errors.Is(err, ErrApplicationNotFound) {
		t.Fatalf("Rotate unknown application error = %v, want ErrApplicationNotFound", err)
	}
	if err := service.Revoke(ctx, unknownApplicationID); !errors.Is(err, ErrApplicationNotFound) {
		t.Fatalf("Revoke unknown application error = %v, want ErrApplicationNotFound", err)
	}
	if err := service.SetEnabled(ctx, unknownApplicationID, false); !errors.Is(err, ErrApplicationNotFound) {
		t.Fatalf("SetEnabled unknown application error = %v, want ErrApplicationNotFound", err)
	}
}

func newCompatAppDisposableDatabase(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate database name: %v", err)
	}
	name := "vondel_compatapp_" + hex.EncodeToString(random[:])

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
		// DROP DATABASE needs exclusive access and can wait a long time when
		// other suites are hammering the same shared Postgres, so the window
		// is generous.
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
