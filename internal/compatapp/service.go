package compatapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Secret prefixes distinguish the two secret families at a glance and let a
// leak scan find an escaped secret anywhere. The prefix constants themselves
// are not secrets.
const (
	enrollmentSecretPrefix  = "vce_"
	serviceCredentialPrefix = "vcc_"
)

// Audit event names, mirrored by the migration's CHECK constraint.
const (
	auditEnrollmentCreated = "enrollment_created"
	auditEnrolled          = "enrolled"
	auditRotated           = "rotated"
	auditRevoked           = "revoked"
	auditEnabled           = "enabled"
	auditDisabled          = "disabled"
)

// Service owns companion enrollment and revocable service trust.
type Service struct {
	store *Store
	// now is injectable so tests can cross TTL boundaries deterministically.
	// Every expiry decision compares stored timestamps against this clock.
	now func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	if pool == nil {
		return nil
	}
	return &Service{store: NewStore(pool), now: time.Now}
}

// CreateEnrollment mints a single-use, 15-minute enrollment secret carrying
// an admin-reviewed capability grant. The raw secret is returned exactly
// once; only its SHA-256 digest is stored.
func (s *Service) CreateEnrollment(ctx context.Context, kind Kind, requested []Capability) (EnrollmentSecret, error) {
	if s == nil || s.store == nil {
		return EnrollmentSecret{}, fmt.Errorf("compat application service is unavailable")
	}
	if _, ok := knownKinds[kind]; !ok {
		return EnrollmentSecret{}, ErrUnknownKind
	}
	capabilities, err := normalizeCapabilities(requested)
	if err != nil {
		return EnrollmentSecret{}, err
	}

	secret, digest, err := newSecret(enrollmentSecretPrefix)
	if err != nil {
		return EnrollmentSecret{}, err
	}
	now := s.now()
	expiresAt := now.Add(EnrollmentTTL)
	enrollmentID := uuid.NewString()
	// One transaction: an enrollment must not exist without its audit row,
	// which is the atomicity every other lifecycle path already has.
	tx, err := s.store.pool.Begin(ctx)
	if err != nil {
		return EnrollmentSecret{}, fmt.Errorf("begin enrollment create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.store.InsertEnrollment(ctx, tx, enrollmentID, string(kind), digest, capabilityStrings(capabilities), expiresAt); err != nil {
		return EnrollmentSecret{}, err
	}
	if err := s.store.InsertAudit(ctx, tx, "", enrollmentID, auditEnrollmentCreated, map[string]any{
		"kind":         string(kind),
		"capabilities": capabilityStrings(capabilities),
	}); err != nil {
		return EnrollmentSecret{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EnrollmentSecret{}, fmt.Errorf("commit enrollment create: %w", err)
	}
	return EnrollmentSecret{
		ID:           enrollmentID,
		Kind:         kind,
		Secret:       secret,
		Capabilities: capabilities,
		ExpiresAt:    expiresAt,
	}, nil
}

// Enroll redeems an enrollment secret for an immutable application identity
// and the first service credential. The enrollment row is consumed under its
// row lock, so a concurrent replay of the same secret admits exactly one
// caller. A request that fails validation rolls back without consuming the
// secret, so a misconfigured companion can retry against the same enrollment.
func (s *Service) Enroll(ctx context.Context, secret string, request EnrollmentRequest) (credential ServiceCredential, err error) {
	if s == nil || s.store == nil {
		return ServiceCredential{}, fmt.Errorf("compat application service is unavailable")
	}
	if !strings.HasPrefix(secret, enrollmentSecretPrefix) {
		return ServiceCredential{}, ErrEnrollmentDenied
	}
	presentedDigest := digestSecret(secret)
	now := s.now()

	tx, err := s.store.Begin(ctx)
	if err != nil {
		return ServiceCredential{}, fmt.Errorf("begin enrollment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	enrollment, err := s.store.LockEnrollmentByDigest(ctx, tx, presentedDigest)
	if err != nil {
		return ServiceCredential{}, err
	}
	// The row was found through an indexed digest lookup; re-verify with a
	// constant-time comparison so no comparison of secret material is ever
	// timing-dependent.
	if subtle.ConstantTimeCompare([]byte(enrollment.SecretDigest), []byte(presentedDigest)) != 1 {
		return ServiceCredential{}, ErrEnrollmentDenied
	}
	if enrollment.ConsumedAt != nil || !enrollment.ExpiresAt.After(now) {
		return ServiceCredential{}, ErrEnrollmentDenied
	}

	instanceID := strings.TrimSpace(request.InstanceID)
	version := strings.TrimSpace(request.Version)
	if instanceID == "" || version == "" {
		return ServiceCredential{}, ErrInvalidEnrollmentRequest
	}
	// The authoritative kind comes from the enrollment secret; a companion
	// declaring a different kind is misconfigured (or holding someone else's
	// secret) and must be refused here, inside the validation block, so the
	// secret survives for an honest retry.
	if request.DeclaredKind != "" && request.DeclaredKind != enrollment.Kind {
		return ServiceCredential{}, ErrKindMismatch
	}
	if request.APIRangeMin < 1 || request.APIRangeMax < request.APIRangeMin {
		return ServiceCredential{}, ErrInvalidEnrollmentRequest
	}
	if request.APIRangeMin > ServerAPIVersion || request.APIRangeMax < ServerAPIVersion {
		return ServiceCredential{}, ErrAPIRangeUnsupported
	}
	granted, err := normalizeCapabilities(request.Capabilities)
	if err != nil {
		return ServiceCredential{}, err
	}
	if err := requireSubset(granted, enrollment.Capabilities); err != nil {
		return ServiceCredential{}, err
	}
	fingerprint := peerCertificateFingerprint(request.PeerTLS)

	applicationID, err := s.store.InsertApplication(
		ctx, tx,
		string(enrollment.Kind), instanceID, version, strings.TrimSpace(request.ImageDigest),
		request.APIRangeMin, request.APIRangeMax,
		capabilityStrings(granted), fingerprint, now,
	)
	if err != nil {
		return ServiceCredential{}, err
	}
	if err := s.store.ConsumeEnrollment(ctx, tx, enrollment.ID, applicationID, now); err != nil {
		return ServiceCredential{}, err
	}

	rawCredential, credentialDigest, err := newSecret(serviceCredentialPrefix)
	if err != nil {
		return ServiceCredential{}, err
	}
	credentialID := uuid.NewString()
	expiresAt := now.Add(CredentialTTL)
	if err := s.store.InsertCredential(ctx, tx, credentialID, applicationID, credentialDigest, expiresAt); err != nil {
		return ServiceCredential{}, err
	}
	if err := s.store.InsertAudit(ctx, tx, applicationID, enrollment.ID, auditEnrolled, map[string]any{
		"kind":         string(enrollment.Kind),
		"instance_id":  instanceID,
		"version":      version,
		"capabilities": capabilityStrings(granted),
	}); err != nil {
		return ServiceCredential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceCredential{}, fmt.Errorf("commit enrollment: %w", err)
	}
	return ServiceCredential{
		ApplicationID: applicationID,
		CredentialID:  credentialID,
		Secret:        rawCredential,
		ExpiresAt:     expiresAt,
		Capabilities:  granted,
	}, nil
}

// Authenticate verifies a bearer service credential, enforces the
// application's trust state, and — when a TLS client certificate was bound at
// enrollment — requires the same certificate on the presented connection.
// Every successful use renews the credential's 15-minute window.
func (s *Service) Authenticate(ctx context.Context, bearer string, peerTLS *tls.ConnectionState) (Identity, error) {
	if s == nil || s.store == nil {
		return Identity{}, fmt.Errorf("compat application service is unavailable")
	}
	if !strings.HasPrefix(bearer, serviceCredentialPrefix) {
		return Identity{}, ErrCredentialInvalid
	}
	presentedDigest := digestSecret(bearer)
	credential, application, err := s.store.CredentialByDigest(ctx, presentedDigest)
	if err != nil {
		return Identity{}, err
	}
	if subtle.ConstantTimeCompare([]byte(credential.SecretDigest), []byte(presentedDigest)) != 1 {
		return Identity{}, ErrCredentialInvalid
	}
	now := s.now()
	switch {
	case application.RevokedAt != nil:
		return Identity{}, ErrApplicationRevoked
	case credential.RevokedAt != nil, !credential.ExpiresAt.After(now):
		return Identity{}, ErrCredentialInvalid
	case !application.Enabled:
		return Identity{}, ErrApplicationDisabled
	case application.APIRangeMin > ServerAPIVersion, application.APIRangeMax < ServerAPIVersion:
		// The range the companion registered at enrollment no longer covers
		// this server's API version; the credential alone must not keep an
		// incompatible companion talking.
		return Identity{}, ErrAPIRangeUnsupported
	}
	if application.TLSFingerprint != nil {
		presented := peerCertificateFingerprint(peerTLS)
		if presented == "" {
			return Identity{}, ErrPeerCertificateRequired
		}
		if subtle.ConstantTimeCompare([]byte(*application.TLSFingerprint), []byte(presented)) != 1 {
			return Identity{}, ErrPeerCertificateMismatch
		}
	}
	if err := s.store.RenewCredential(ctx, credential.ID, now.Add(CredentialTTL), application.ID, now); err != nil {
		return Identity{}, err
	}
	return Identity{
		ApplicationID: application.ID,
		Kind:          application.Kind,
		InstanceID:    application.InstanceID,
		CredentialID:  credential.ID,
		Capabilities:  toCapabilities(application.Capabilities),
	}, nil
}

// Rotate revokes every outstanding credential for one application and issues
// a fresh one. Other applications are untouched.
//
// This is the companion's own renewal path: it runs on the credential window,
// not on an administrator's decision, so it deliberately does not advance the
// application's administrative revision. See RotateByInstance for the
// administrator-forced rotation.
func (s *Service) Rotate(ctx context.Context, applicationID string) (credential ServiceCredential, err error) {
	if s == nil || s.store == nil {
		return ServiceCredential{}, fmt.Errorf("compat application service is unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return ServiceCredential{}, fmt.Errorf("begin rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	application, err := s.lockLiveApplication(ctx, tx, applicationID)
	if err != nil {
		return ServiceCredential{}, err
	}
	rotated, err := s.rotateLocked(ctx, tx, application, false)
	if err != nil {
		return ServiceCredential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceCredential{}, fmt.Errorf("commit rotation: %w", err)
	}
	return rotated, nil
}

// RotateByInstance is the administrator-forced rotation behind the admin
// surface. It refuses a decision taken against a stale revision, and — unlike
// companion self-renewal — records the rotation on the application row, which
// is what advances the revision. Without that, two administrators rotating
// from the same page would both succeed and the first one's freshly issued
// secret would be dead before it was ever used.
func (s *Service) RotateByInstance(ctx context.Context, instanceID string, expectedRevision int64) (ServiceCredential, Application, error) {
	if s == nil || s.store == nil {
		return ServiceCredential{}, Application{}, fmt.Errorf("compat application service is unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return ServiceCredential{}, Application{}, fmt.Errorf("begin rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	application, err := s.decidableApplication(ctx, tx, instanceID, expectedRevision)
	if err != nil {
		return ServiceCredential{}, Application{}, err
	}
	rotated, err := s.rotateLocked(ctx, tx, application, true)
	if err != nil {
		return ServiceCredential{}, Application{}, err
	}
	view, err := s.store.ApplicationVia(ctx, tx, application.ID)
	if err != nil {
		return ServiceCredential{}, Application{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceCredential{}, Application{}, fmt.Errorf("commit rotation: %w", err)
	}
	return rotated, view, nil
}

// Revoke permanently withdraws an application's trust and every credential it
// holds. Revoking an already revoked application is a no-op.
func (s *Service) Revoke(ctx context.Context, applicationID string) (err error) {
	if s == nil || s.store == nil {
		return fmt.Errorf("compat application service is unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	application, err := s.store.LockApplication(ctx, tx, applicationID)
	if err != nil {
		return err
	}
	if err := s.revokeLocked(ctx, tx, application); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit revocation: %w", err)
	}
	return nil
}

// RevokeByInstance is Revoke behind the admin surface's revision guard.
// Revocation is terminal, so replaying it against the current revision
// settles instead of failing: the administrator asked for a state the
// application is already in.
func (s *Service) RevokeByInstance(ctx context.Context, instanceID string, expectedRevision int64) (Application, error) {
	if s == nil || s.store == nil {
		return Application{}, fmt.Errorf("compat application service is unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return Application{}, fmt.Errorf("begin revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	application, err := s.guardedApplication(ctx, tx, instanceID, expectedRevision)
	if err != nil {
		return Application{}, err
	}
	if err := s.revokeLocked(ctx, tx, application); err != nil {
		return Application{}, err
	}
	view, err := s.store.ApplicationVia(ctx, tx, application.ID)
	if err != nil {
		return Application{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Application{}, fmt.Errorf("commit revocation: %w", err)
	}
	return view, nil
}

// SetEnabled flips the reversible off switch. A no-op transition succeeds
// without an audit row; a revoked application cannot be re-enabled or
// disabled.
func (s *Service) SetEnabled(ctx context.Context, applicationID string, enabled bool) (err error) {
	if s == nil || s.store == nil {
		return fmt.Errorf("compat application service is unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin enablement update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	application, err := s.lockLiveApplication(ctx, tx, applicationID)
	if err != nil {
		return err
	}
	if err := s.setEnabledLocked(ctx, tx, application, enabled); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit enablement update: %w", err)
	}
	return nil
}

// SetEnabledByInstance is SetEnabled behind the admin surface's revision
// guard, addressed by the instance identity the operator sees.
func (s *Service) SetEnabledByInstance(ctx context.Context, instanceID string, enabled bool, expectedRevision int64) (Application, error) {
	if s == nil || s.store == nil {
		return Application{}, fmt.Errorf("compat application service is unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return Application{}, fmt.Errorf("begin enablement update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	application, err := s.decidableApplication(ctx, tx, instanceID, expectedRevision)
	if err != nil {
		return Application{}, err
	}
	if err := s.setEnabledLocked(ctx, tx, application, enabled); err != nil {
		return Application{}, err
	}
	view, err := s.store.ApplicationVia(ctx, tx, application.ID)
	if err != nil {
		return Application{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Application{}, fmt.Errorf("commit enablement update: %w", err)
	}
	return view, nil
}

// ListApplications returns the administration view of every enrolled
// application. It reads the application table alone, so no credential —
// raw or digested — can travel with it.
func (s *Service) ListApplications(ctx context.Context) ([]Application, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("compat application service is unavailable")
	}
	return s.store.ListApplications(ctx)
}

// guardedApplication locks an application by the instance identity the admin
// surface addresses and refuses a decision that was not taken against the
// revision the row currently carries.
//
// The lock is the whole concurrency model. Two administrators deciding from
// the same page serialize on this row; the second one's SELECT ... FOR UPDATE
// only returns once the first has committed, and it returns the committed
// row, so its guard compares against the revision the winner produced.
func (s *Service) guardedApplication(ctx context.Context, tx pgx.Tx, instanceID string, expectedRevision int64) (applicationRow, error) {
	application, err := s.store.LockApplicationByInstance(ctx, tx, instanceID)
	if err != nil {
		return applicationRow{}, err
	}
	if application.Revision != expectedRevision {
		return applicationRow{}, &RevisionMismatchError{
			InstanceID: application.InstanceID,
			Expected:   expectedRevision,
			Current:    application.Revision,
		}
	}
	return application, nil
}

// decidableApplication is guardedApplication for the decisions revocation
// makes meaningless. The revision is checked first: an administrator who was
// looking at the application before it was revoked gets the more useful
// "reload, it moved" answer, because revocation itself advances the revision.
func (s *Service) decidableApplication(ctx context.Context, tx pgx.Tx, instanceID string, expectedRevision int64) (applicationRow, error) {
	application, err := s.guardedApplication(ctx, tx, instanceID, expectedRevision)
	if err != nil {
		return applicationRow{}, err
	}
	if application.RevokedAt != nil {
		return applicationRow{}, &ApplicationRevokedError{
			InstanceID: application.InstanceID,
			Current:    application.Revision,
		}
	}
	return application, nil
}

// setEnabledLocked applies the enablement decision to an already-locked row.
// A no-op transition decided nothing, so it writes neither state nor audit.
func (s *Service) setEnabledLocked(ctx context.Context, tx pgx.Tx, application applicationRow, enabled bool) error {
	if application.Enabled == enabled {
		return nil
	}
	if err := s.store.SetApplicationEnabled(ctx, tx, application.ID, enabled); err != nil {
		return err
	}
	event := auditDisabled
	if enabled {
		event = auditEnabled
	}
	return s.store.InsertAudit(ctx, tx, application.ID, "", event, nil)
}

// revokeLocked withdraws trust from an already-locked row, settling quietly
// when the application is already revoked.
func (s *Service) revokeLocked(ctx context.Context, tx pgx.Tx, application applicationRow) error {
	if application.RevokedAt != nil {
		return nil
	}
	now := s.now()
	if err := s.store.MarkApplicationRevoked(ctx, tx, application.ID, now); err != nil {
		return err
	}
	if err := s.store.RevokeCredentials(ctx, tx, application.ID, now); err != nil {
		return err
	}
	return s.store.InsertAudit(ctx, tx, application.ID, "", auditRevoked, nil)
}

// rotateLocked kills every outstanding credential for an already-locked row
// and issues one fresh credential. administrative marks the rotation on the
// application row, which is what makes it visible to the revision guard.
func (s *Service) rotateLocked(ctx context.Context, tx pgx.Tx, application applicationRow, administrative bool) (ServiceCredential, error) {
	now := s.now()
	if err := s.store.RevokeCredentials(ctx, tx, application.ID, now); err != nil {
		return ServiceCredential{}, err
	}
	rawCredential, credentialDigest, err := newSecret(serviceCredentialPrefix)
	if err != nil {
		return ServiceCredential{}, err
	}
	credentialID := uuid.NewString()
	expiresAt := now.Add(CredentialTTL)
	if err := s.store.InsertCredential(ctx, tx, credentialID, application.ID, credentialDigest, expiresAt); err != nil {
		return ServiceCredential{}, err
	}
	if administrative {
		if err := s.store.MarkCredentialRotated(ctx, tx, application.ID, now); err != nil {
			return ServiceCredential{}, err
		}
	}
	if err := s.store.InsertAudit(ctx, tx, application.ID, "", auditRotated, nil); err != nil {
		return ServiceCredential{}, err
	}
	return ServiceCredential{
		ApplicationID: application.ID,
		CredentialID:  credentialID,
		Secret:        rawCredential,
		ExpiresAt:     expiresAt,
		Capabilities:  toCapabilities(application.Capabilities),
	}, nil
}

// Heartbeat records companion-reported health and last contact.
func (s *Service) Heartbeat(ctx context.Context, applicationID string, health HealthStatus) (err error) {
	if s == nil || s.store == nil {
		return fmt.Errorf("compat application service is unavailable")
	}
	if _, ok := knownHealthStatuses[health]; !ok {
		return ErrInvalidHealthStatus
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin heartbeat: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	application, err := s.lockLiveApplication(ctx, tx, applicationID)
	if err != nil {
		return err
	}
	if err := s.store.RecordHeartbeat(ctx, tx, application.ID, health, s.now()); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit heartbeat: %w", err)
	}
	return nil
}

// lockLiveApplication locks an application row and refuses a revoked one.
func (s *Service) lockLiveApplication(ctx context.Context, tx pgx.Tx, applicationID string) (applicationRow, error) {
	application, err := s.store.LockApplication(ctx, tx, applicationID)
	if err != nil {
		return applicationRow{}, err
	}
	if application.RevokedAt != nil {
		return applicationRow{}, ErrApplicationRevoked
	}
	return application, nil
}

// newSecret mints a prefixed 256-bit random secret and its storable SHA-256
// hex digest. Callers hand the raw value out exactly once and persist only
// the digest.
func newSecret(prefix string) (raw string, digest string, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", fmt.Errorf("generate secret: %w", err)
	}
	raw = prefix + base64.RawURLEncoding.EncodeToString(buf[:])
	return raw, digestSecret(raw), nil
}

func digestSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// peerCertificateFingerprint returns the SHA-256 hex fingerprint of the
// connection's leaf client certificate, or "" when none was presented.
func peerCertificateFingerprint(state *tls.ConnectionState) string {
	if state == nil || len(state.PeerCertificates) == 0 {
		return ""
	}
	sum := sha256.Sum256(state.PeerCertificates[0].Raw)
	return hex.EncodeToString(sum[:])
}

// normalizeCapabilities deduplicates and sorts a capability set, failing
// closed on an empty set or any name outside the reviewed registry.
func normalizeCapabilities(requested []Capability) ([]Capability, error) {
	seen := make(map[Capability]struct{}, len(requested))
	normalized := make([]Capability, 0, len(requested))
	for _, capability := range requested {
		if _, ok := knownCapabilities[capability]; !ok {
			return nil, ErrUnknownCapability
		}
		if _, dup := seen[capability]; dup {
			continue
		}
		seen[capability] = struct{}{}
		normalized = append(normalized, capability)
	}
	if len(normalized) == 0 {
		return nil, ErrNoCapabilities
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized, nil
}

// requireSubset fails closed unless every requested capability appears in the
// enrollment's reviewed grant.
func requireSubset(requested []Capability, granted []string) error {
	allowed := make(map[string]struct{}, len(granted))
	for _, capability := range granted {
		allowed[capability] = struct{}{}
	}
	for _, capability := range requested {
		if _, ok := allowed[string(capability)]; !ok {
			return ErrCapabilityNotGranted
		}
	}
	return nil
}

func capabilityStrings(capabilities []Capability) []string {
	values := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		values = append(values, string(capability))
	}
	return values
}
