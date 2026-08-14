// Package compatapp owns companion enrollment and revocable service trust for
// the removable compatibility applications (vondel-audiobookshelf and
// vondel-jellyfin). An administrator mints a short-lived single-use enrollment
// secret carrying a reviewed capability grant; the companion redeems it once
// for an immutable application identity and a renewable short-lived service
// credential. Raw secrets leave this package exactly once, at issuance; only
// SHA-256 digests are stored.
package compatapp

import (
	"crypto/tls"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Kind names the external protocol a companion implements. It is fixed at
// enrollment and immutable afterwards.
type Kind string

const (
	KindAudiobookshelf Kind = "audiobookshelf"
	KindJellyfin       Kind = "jellyfin"
)

var knownKinds = map[Kind]struct{}{
	KindAudiobookshelf: {},
	KindJellyfin:       {},
}

// Capability names one reviewed slice of the private compatibility API. The
// set is closed: a name outside it fails enrollment, because a companion must
// never receive an authorization the reviewer could not have understood.
//
// The vocabulary is the OpenAPI contract's (contracts/compat/v1/openapi.yaml,
// the frozen public artifact): one slug per x-vondel-capability value. The
// contract's implicit "service" capability — enrollment, renewal, health —
// belongs to every enrolled application and is deliberately not grantable
// here. A lockstep test in internal/compatapi pins the two vocabularies to
// each other.
type Capability string

const (
	// CapabilityIdentity covers credential exchange, device trust, profile
	// discovery, PIN verification, and profile switching.
	CapabilityIdentity Capability = "identity"
	// CapabilityCatalog covers libraries, browse, search, detail, metadata
	// reads, and artwork/signed resource resolution.
	CapabilityCatalog Capability = "catalog"
	// CapabilityState covers progress, watched state, favorites, bookmarks,
	// collections, playlists, and downloads.
	CapabilityState Capability = "state"
	// CapabilityPlayback covers playback planning, stream authorization,
	// cancellation, recovery, and live session/device reporting.
	CapabilityPlayback Capability = "playback"
	// CapabilityLiveTV covers Live TV channels, guide, tuner availability,
	// stream authorization, DVR rules, and recordings.
	CapabilityLiveTV Capability = "livetv"
	// CapabilityEvents covers subject-filtered events with resumable
	// cursors.
	CapabilityEvents Capability = "events"
)

var knownCapabilities = map[Capability]struct{}{
	CapabilityIdentity: {},
	CapabilityCatalog:  {},
	CapabilityState:    {},
	CapabilityPlayback: {},
	CapabilityLiveTV:   {},
	CapabilityEvents:   {},
}

// GrantableCapabilities returns the closed, sorted registry of capabilities
// an enrollment may grant.
func GrantableCapabilities() []Capability {
	out := make([]Capability, 0, len(knownCapabilities))
	for capability := range knownCapabilities {
		out = append(out, capability)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// HealthStatus is the companion-reported health recorded by Heartbeat.
type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
)

var knownHealthStatuses = map[HealthStatus]struct{}{
	HealthUnknown:   {},
	HealthHealthy:   {},
	HealthDegraded:  {},
	HealthUnhealthy: {},
}

// ServerAPIVersion is the compatibility API version this server implements.
// Enrollment and authentication both fail closed when the companion's
// registered [min, max] range excludes it.
const ServerAPIVersion = 1

const (
	// EnrollmentTTL bounds how long an unconsumed enrollment secret stays
	// redeemable.
	EnrollmentTTL = 15 * time.Minute
	// CredentialTTL bounds how long a service credential stays valid after
	// its last successful authentication; each use renews the window.
	CredentialTTL = 15 * time.Minute
)

// EnrollmentSecret is the one-time result of CreateEnrollment. Secret is the
// only copy of the raw enrollment secret that will ever exist; the database
// keeps its digest.
type EnrollmentSecret struct {
	ID           string
	Kind         Kind
	Secret       string
	Capabilities []Capability
	ExpiresAt    time.Time
}

// EnrollmentRequest is what a companion registers when redeeming an
// enrollment secret.
type EnrollmentRequest struct {
	InstanceID  string
	Version     string
	ImageDigest string
	APIRangeMin int
	APIRangeMax int
	// DeclaredKind is the kind the companion believes it is enrolling as.
	// The authoritative kind comes from the enrollment secret; a non-empty
	// declaration that contradicts it fails with ErrKindMismatch without
	// consuming the secret, so a swapped or mis-mounted secret is caught at
	// the door instead of silently enrolling the wrong protocol.
	DeclaredKind Kind
	Capabilities []Capability
	// PeerTLS carries the TLS state of the enrolling connection, when any.
	// If a client certificate was presented, its fingerprint is bound to the
	// application and required on every later authentication.
	PeerTLS *tls.ConnectionState
}

// ServiceCredential is the one-time result of Enroll and Rotate. Secret is
// the only copy of the raw service credential that will ever exist.
type ServiceCredential struct {
	ApplicationID string
	CredentialID  string
	Secret        string
	ExpiresAt     time.Time
	Capabilities  []Capability
}

// Identity is the authenticated caller of the private compatibility API.
type Identity struct {
	ApplicationID string
	Kind          Kind
	InstanceID    string
	CredentialID  string
	Capabilities  []Capability
}

// Application is the durable trust record for one enrolled companion
// instance, as the administration surface sees it. It carries no secret
// material: credentials live in their own table as digests and never reach
// this struct.
type Application struct {
	ID             string
	Kind           Kind
	InstanceID     string
	Version        string
	ImageDigest    string
	APIRangeMin    int
	APIRangeMax    int
	Capabilities   []Capability
	TLSFingerprint string
	Enabled        bool
	Health         HealthStatus
	LastContactAt  *time.Time
	RevokedAt      *time.Time
	// CredentialRotatedAt is when an administrator last forced a rotation.
	// Companion self-renewal does not set it.
	CredentialRotatedAt *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	// Revision is the optimistic-concurrency token the administration
	// surface decides against. The database derives it: it advances by one
	// on every administrative change to this application and on nothing
	// else. See migrations/sql/20260813200400_compat_application_revision.sql.
	Revision int64
}

// AuditEvent is one immutable trust-lifecycle record.
type AuditEvent struct {
	ID            int64
	ApplicationID string
	EnrollmentID  string
	Event         string
	CreatedAt     time.Time
}

// RevisionMismatchError reports an administrative decision taken against a
// revision the application has already moved past — the optimistic-concurrency
// conflict the admin surface renders as 409. Current is the revision to
// reload and retry against.
type RevisionMismatchError struct {
	InstanceID string
	Expected   int64
	Current    int64
}

func (e *RevisionMismatchError) Error() string {
	return fmt.Sprintf("compat application %q is at revision %d, not the expected %d", e.InstanceID, e.Current, e.Expected)
}

// Unwrap lets callers match the sentinel while errors.As still recovers the
// revision to retry against.
func (e *RevisionMismatchError) Unwrap() error { return ErrRevisionMismatch }

// ApplicationRevokedError reports a decision taken against an application
// whose trust was permanently withdrawn. It carries the current revision for
// the same reason RevisionMismatchError does: the caller's next move is to
// reload, and revocation is terminal state it has not seen yet.
type ApplicationRevokedError struct {
	InstanceID string
	Current    int64
}

func (e *ApplicationRevokedError) Error() string {
	return fmt.Sprintf("compat application %q is revoked (revision %d)", e.InstanceID, e.Current)
}

func (e *ApplicationRevokedError) Unwrap() error { return ErrApplicationRevoked }

var (
	ErrRevisionMismatch         = errors.New("compat application revision no longer matches")
	ErrUnknownKind              = errors.New("unknown compatibility application kind")
	ErrUnknownCapability        = errors.New("unknown compatibility capability")
	ErrNoCapabilities           = errors.New("at least one capability is required")
	ErrCapabilityNotGranted     = errors.New("requested capability is outside the enrollment grant")
	ErrEnrollmentDenied         = errors.New("enrollment secret is unknown, expired, or already used")
	ErrInvalidEnrollmentRequest = errors.New("enrollment request is malformed")
	ErrKindMismatch             = errors.New("declared kind contradicts the enrollment secret")
	ErrAPIRangeUnsupported      = errors.New("companion API range excludes the server version")
	ErrInstanceAlreadyEnrolled  = errors.New("application instance is already enrolled")
	ErrCredentialInvalid        = errors.New("service credential is unknown, expired, or revoked")
	ErrApplicationDisabled      = errors.New("application is disabled")
	ErrApplicationRevoked       = errors.New("application is revoked")
	ErrApplicationNotFound      = errors.New("application not found")
	ErrPeerCertificateRequired  = errors.New("a TLS client certificate is required for this application")
	ErrPeerCertificateMismatch  = errors.New("TLS client certificate does not match the enrolled identity")
	ErrInvalidHealthStatus      = errors.New("unknown health status")
)

// Sentinels is every error this package returns as a named value. Callers that
// translate these onto their own vocabulary — the private API adapter does —
// assert against this list, so a sentinel added here fails their test rather
// than silently reaching a caller's default arm as an opaque server error.
func Sentinels() []error {
	return []error{
		ErrRevisionMismatch,
		ErrUnknownKind,
		ErrUnknownCapability,
		ErrNoCapabilities,
		ErrCapabilityNotGranted,
		ErrEnrollmentDenied,
		ErrInvalidEnrollmentRequest,
		ErrKindMismatch,
		ErrAPIRangeUnsupported,
		ErrInstanceAlreadyEnrolled,
		ErrCredentialInvalid,
		ErrApplicationDisabled,
		ErrApplicationRevoked,
		ErrApplicationNotFound,
		ErrPeerCertificateRequired,
		ErrPeerCertificateMismatch,
		ErrInvalidHealthStatus,
	}
}
