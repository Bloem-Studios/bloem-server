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
type Capability string

const (
	// CapabilityIdentityExchange covers credential exchange, device trust,
	// profile discovery, PIN verification, and profile switching.
	CapabilityIdentityExchange Capability = "identity.exchange"
	// CapabilityCatalogRead covers libraries, browse, search, detail, and
	// metadata reads.
	CapabilityCatalogRead Capability = "catalog.read"
	// CapabilityArtworkRead covers artwork and signed resource resolution.
	CapabilityArtworkRead Capability = "artwork.read"
	// CapabilityStateReadWrite covers progress, watched state, favorites,
	// bookmarks, collections, playlists, and downloads.
	CapabilityStateReadWrite Capability = "state.readwrite"
	// CapabilityPlaybackStream covers playback planning, stream
	// authorization, cancellation, and recovery.
	CapabilityPlaybackStream Capability = "playback.stream"
	// CapabilitySessionsReport covers live session and device reporting.
	CapabilitySessionsReport Capability = "sessions.report"
	// CapabilityLiveTVAccess covers Live TV channels, guide, stream
	// authorization, DVR rules, and recordings.
	CapabilityLiveTVAccess Capability = "livetv.access"
	// CapabilityEventsSubscribe covers subject-filtered events with
	// resumable cursors.
	CapabilityEventsSubscribe Capability = "events.subscribe"
)

var knownCapabilities = map[Capability]struct{}{
	CapabilityIdentityExchange: {},
	CapabilityCatalogRead:      {},
	CapabilityArtworkRead:      {},
	CapabilityStateReadWrite:   {},
	CapabilityPlaybackStream:   {},
	CapabilitySessionsReport:   {},
	CapabilityLiveTVAccess:     {},
	CapabilityEventsSubscribe:  {},
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
	InstanceID   string
	Version      string
	ImageDigest  string
	APIRangeMin  int
	APIRangeMax  int
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
// instance, as the administration surface sees it.
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
	CreatedAt      time.Time
}

// AuditEvent is one immutable trust-lifecycle record.
type AuditEvent struct {
	ID            int64
	ApplicationID string
	EnrollmentID  string
	Event         string
	CreatedAt     time.Time
}

var (
	ErrUnknownKind              = errors.New("unknown compatibility application kind")
	ErrUnknownCapability        = errors.New("unknown compatibility capability")
	ErrNoCapabilities           = errors.New("at least one capability is required")
	ErrCapabilityNotGranted     = errors.New("requested capability is outside the enrollment grant")
	ErrEnrollmentDenied         = errors.New("enrollment secret is unknown, expired, or already used")
	ErrInvalidEnrollmentRequest = errors.New("enrollment request is malformed")
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
