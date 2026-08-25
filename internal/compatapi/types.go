// Package compatapi implements the private Compatibility Service API v1
// consumed exclusively by enrolled compatibility applications
// (bloem-audiobookshelf, bloem-jellyfin).
//
// The package is a policy-enforcing adapter layer: every handler
// authenticates the calling application, validates the operation's
// capability, resolves the acting subject exclusively from the signed,
// audience-bound subject token, revalidates that subject against
// authoritative server state, and only then invokes the narrow domain
// interfaces defined here. Companions never receive database identifiers
// outside the public media model, filesystem paths, provider or tuner
// credentials, or signing material.
//
// The wire contract lives in contracts/compat/v1/openapi.yaml; the handler
// test suite pins route/capability/idempotency parity against it.
package compatapi

import (
	"context"
	"crypto/tls"
	"errors"
	"time"
)

// Capability slugs. They must match the x-bloem-capability values in the
// OpenAPI contract and the capability vocabulary of the enrollment service.
const (
	// CapabilityService is the implicit base capability every enrolled
	// application holds: enrollment, credential renewal, and health.
	CapabilityService  = "service"
	CapabilityIdentity = "identity"
	CapabilityCatalog  = "catalog"
	CapabilityState    = "state"
	CapabilityPlayback = "playback"
	CapabilityLiveTV   = "livetv"
	CapabilityEvents   = "events"
)

// Login grant types accepted by the identity surface.
const (
	GrantTypeDirectProfile = "direct_profile"
	GrantTypeAccount       = "account"
)

// KnownCapabilities returns the full reviewed capability vocabulary.
func KnownCapabilities() []string {
	return []string{
		CapabilityService,
		CapabilityIdentity,
		CapabilityCatalog,
		CapabilityState,
		CapabilityPlayback,
		CapabilityLiveTV,
		CapabilityEvents,
	}
}

// Sentinel errors domain services return; the handler maps them onto the
// stable error envelope. Any other error becomes an opaque internal error so
// driver or SQL detail can never reach a companion.
var (
	ErrNotFound           = errors.New("compatapi: not found")
	ErrUnavailable        = errors.New("compatapi: unavailable")
	ErrConflict           = errors.New("compatapi: conflict")
	ErrInvalid            = errors.New("compatapi: invalid request")
	ErrForbidden          = errors.New("compatapi: forbidden")
	ErrInvalidCredentials = errors.New("compatapi: invalid credentials")
	ErrSubjectRevoked     = errors.New("compatapi: subject revoked")
	ErrPINInvalid         = errors.New("compatapi: pin invalid")
)

// AppIdentity is the authenticated identity of a calling compatibility
// application. It mirrors the enrollment service's identity facts
// (application id, kind, instance id, granted capabilities) through a local
// type so this package depends only on the narrow AppAuthenticator interface.
type AppIdentity struct {
	ApplicationID string
	Kind          string // audiobookshelf | jellyfin
	InstanceID    string
	Capabilities  []string
}

// HasCapability reports whether the application was granted the slug.
// CapabilityService is implicit for every authenticated application.
func (id AppIdentity) HasCapability(slug string) bool {
	if slug == CapabilityService {
		return true
	}
	for _, granted := range id.Capabilities {
		if granted == slug {
			return true
		}
	}
	return false
}

// AppAuthenticator authenticates a service bearer credential, optionally
// bound to the peer's TLS identity for remote placements. Its signature
// matches the enrollment service's Authenticate so wiring is a one-line
// adapter.
type AppAuthenticator interface {
	Authenticate(ctx context.Context, bearer string, peerTLS *tls.ConnectionState) (AppIdentity, error)
}

// APIRange is the closed [Min, Max] range of API majors a party supports.
type APIRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// EnrollmentRequest is a companion's one-time registration. Kind is the
// companion's declaration of what it is enrolling as; the authoritative kind
// comes from the enrollment secret, and a contradiction is refused.
type EnrollmentRequest struct {
	Kind                  string   `json:"kind"`
	InstanceID            string   `json:"instance_id"`
	Version               string   `json:"version"`
	ImageDigest           string   `json:"image_digest,omitempty"`
	API                   APIRange `json:"api"`
	RequestedCapabilities []string `json:"requested_capabilities"`
}

// ServiceCredential is a renewable short-lived service credential. The raw
// token appears exactly once per issue.
type ServiceCredential struct {
	ApplicationID       string    `json:"application_id"`
	Token               string    `json:"token"`
	ExpiresAt           time.Time `json:"expires_at"`
	GrantedCapabilities []string  `json:"granted_capabilities,omitempty"`
}

// AppEnroller consumes a one-use enrollment secret. peerTLS carries the
// enrolling connection's TLS state so a presented client certificate is
// bound to the application and required on every later authentication.
type AppEnroller interface {
	Enroll(ctx context.Context, secret string, req EnrollmentRequest, peerTLS *tls.ConnectionState) (ServiceCredential, error)
}

// CredentialRenewer issues a fresh service credential to an application whose
// current credential the middleware has already authenticated — including its
// TLS binding — so renewal never re-authenticates with a different (or
// absent) connection state.
type CredentialRenewer interface {
	Renew(ctx context.Context, applicationID string) (ServiceCredential, error)
}

// HealthStatus is the companion-reported health carried by a health probe.
type HealthStatus string

const (
	// HealthUnknown is recorded for a probe that reports no status: contact
	// only, no health claim.
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
)

// HeartbeatRecorder records companion liveness and reported health; the
// health endpoint reports through it.
type HeartbeatRecorder interface {
	Heartbeat(ctx context.Context, applicationID string, status HealthStatus, at time.Time) error
}

// DeviceClaim identifies the end-user device behind a compatibility session.
// Mirrors the auth package's DeviceClaim field-for-field.
type DeviceClaim struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Platform  string `json:"platform,omitempty"`
	IPAddress string `json:"ip_address,omitempty"`
}

// Subject carries every authoritative fact a compatibility session is bound
// to. It mirrors the auth package's SessionSubject field-for-field: account,
// profile, organization, membership, device, authentication method, and the
// policy/security/credential revisions used for revocation.
type Subject struct {
	AccountID          int
	ProfileID          string
	OrganizationID     string
	MembershipID       string
	PolicyRevision     int64
	SecurityRevision   int64
	CredentialRevision int64
	Device             DeviceClaim
	AuthMethod         string
}

// ProfileTile is one entry of a trusted device's profile picker. Unknown
// devices receive none.
type ProfileTile struct {
	ProfileID    string `json:"profile_id"`
	Name         string `json:"name"`
	PINProtected bool   `json:"pin_protected"`
}

// SubjectService owns end-user authentication and per-request subject
// revalidation. Bloem remains authoritative: CurrentSubject is consulted on
// every subject-scoped request, so profile or organization suspension,
// password reset, policy revision, or device revocation invalidates access
// without trusting companion cache state.
type SubjectService interface {
	// LoginDirect exchanges a direct profile email/password pair.
	LoginDirect(ctx context.Context, email, password string, device DeviceClaim) (Subject, error)
	// LoginAccount performs legacy account login, resolving the remembered
	// or default profile for the device.
	LoginAccount(ctx context.Context, username, password string, device DeviceClaim) (Subject, error)
	// CurrentSubject revalidates a previously issued subject against
	// authoritative state. It returns ErrSubjectRevoked when any bound fact
	// or revision is no longer current.
	CurrentSubject(ctx context.Context, subject Subject) (Subject, error)
	// DeviceProfiles lists the profile tiles the subject's trusted device
	// is authorized to show.
	DeviceProfiles(ctx context.Context, subject Subject) ([]ProfileTile, error)
	// VerifyPIN checks a profile switching PIN.
	VerifyPIN(ctx context.Context, subject Subject, profileID, pin string) error
	// SwitchProfile switches the subject to another authorized profile.
	SwitchProfile(ctx context.Context, subject Subject, profileID, pin string) (Subject, error)
	// Logout ends the subject's session.
	Logout(ctx context.Context, subject Subject) error
}

// --- catalog ---------------------------------------------------------------

// Library is a policy-visible library.
type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// PersonRef names a person's involvement with an item.
type PersonRef struct {
	PersonID string `json:"person_id"`
	Name     string `json:"name"`
	Role     string `json:"role,omitempty"`
	Type     string `json:"type,omitempty"`
}

// ArtworkRef advertises artwork availability; bytes resolve through
// resolveArtwork delivery grants.
type ArtworkRef struct {
	Kind string `json:"kind"`
}

// Item is the public media model projection of one catalog entry. It never
// carries filesystem locations or non-public identifiers.
type Item struct {
	ID                string            `json:"id"`
	Kind              string            `json:"kind"`
	LibraryID         string            `json:"library_id,omitempty"`
	Title             string            `json:"title"`
	SortTitle         string            `json:"sort_title,omitempty"`
	Year              int               `json:"year,omitempty"`
	Overview          string            `json:"overview,omitempty"`
	RuntimeSeconds    int64             `json:"runtime_seconds,omitempty"`
	SeriesID          string            `json:"series_id,omitempty"`
	SeasonID          string            `json:"season_id,omitempty"`
	IndexNumber       int               `json:"index_number,omitempty"`
	ParentIndexNumber int               `json:"parent_index_number,omitempty"`
	ProviderIDs       map[string]string `json:"provider_ids,omitempty"`
	OfficialRating    string            `json:"official_rating,omitempty"`
	CommunityRating   float64           `json:"community_rating,omitempty"`
	Genres            []string          `json:"genres,omitempty"`
	People            []PersonRef       `json:"people,omitempty"`
	Artwork           []ArtworkRef      `json:"artwork,omitempty"`
}

// ItemPage is one page of items with the domain's plain continuation token.
// The handler signs the token before it reaches the wire.
type ItemPage struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Person is a person detail record.
type Person struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Overview string `json:"overview,omitempty"`
}

// ItemQuery filters a catalog browse.
type ItemQuery struct {
	LibraryID string
	Kind      string
	Sort      string
	Cursor    string
	Limit     int
}

// SearchQuery filters a catalog search.
type SearchQuery struct {
	Query  string
	Cursor string
	Limit  int
}

// DeliveryGrant is a short-lived, single-purpose, audience-bound, same-origin
// media delivery authorization. Media bytes never round-trip through this
// API; the companion or its client fetches the granted Bloem URL directly.
type DeliveryGrant struct {
	URL       string    `json:"url"`
	Method    string    `json:"method"`
	ExpiresAt time.Time `json:"expires_at"`
	Audience  string    `json:"audience"`
}

// CatalogService is the narrow catalog surface. Implementations enforce
// organization, profile, and adult-content policy before returning: hidden
// content is ErrNotFound, absent from pages, and absent from counts.
type CatalogService interface {
	Libraries(ctx context.Context, subject Subject) ([]Library, error)
	Items(ctx context.Context, subject Subject, q ItemQuery) (ItemPage, error)
	Item(ctx context.Context, subject Subject, itemID string) (Item, error)
	Children(ctx context.Context, subject Subject, itemID string, q ItemQuery) (ItemPage, error)
	Search(ctx context.Context, subject Subject, q SearchQuery) (ItemPage, error)
	Person(ctx context.Context, subject Subject, personID string) (Person, error)
	// ArtworkGrant mints a delivery grant for the named audience
	// (the calling application's id).
	ArtworkGrant(ctx context.Context, subject Subject, audience, itemID, kind string) (DeliveryGrant, error)
}

// --- state -----------------------------------------------------------------

// Progress is canonical play/read/listen state.
type Progress struct {
	ItemID          string    `json:"item_id"`
	PositionSeconds float64   `json:"position_seconds"`
	DurationSeconds float64   `json:"duration_seconds,omitempty"`
	Watched         bool      `json:"watched,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitzero"`
}

// ProgressUpdate is a progress write.
type ProgressUpdate struct {
	PositionSeconds float64 `json:"position_seconds"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
}

// Bookmark is a canonical bookmark.
type Bookmark struct {
	ID              string    `json:"id"`
	ItemID          string    `json:"item_id"`
	PositionSeconds float64   `json:"position_seconds"`
	Title           string    `json:"title,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitzero"`
}

// BookmarkCreate is a bookmark write.
type BookmarkCreate struct {
	ItemID          string  `json:"item_id"`
	PositionSeconds float64 `json:"position_seconds"`
	Title           string  `json:"title,omitempty"`
}

// Collection is a policy-visible collection.
type Collection struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind,omitempty"`
	ItemIDs []string `json:"item_ids,omitempty"`
}

// CollectionPage is one page of collections.
type CollectionPage struct {
	Collections []Collection `json:"collections"`
	NextCursor  string       `json:"next_cursor,omitempty"`
}

// Playlist is a subject-owned playlist.
type Playlist struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	ItemIDs []string `json:"item_ids,omitempty"`
}

// PlaylistPage is one page of playlists.
type PlaylistPage struct {
	Playlists  []Playlist `json:"playlists"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// PlaylistWrite creates or replaces a playlist.
type PlaylistWrite struct {
	Name    string   `json:"name"`
	ItemIDs []string `json:"item_ids,omitempty"`
}

// Download is a device-bound offline download.
type Download struct {
	ID     string         `json:"id"`
	ItemID string         `json:"item_id"`
	State  string         `json:"state"`
	Grant  *DeliveryGrant `json:"grant,omitempty"`
}

// StateService is the narrow canonical-state surface: progress, watched
// state, favorites, bookmarks, collections, playlists, and downloads all
// remain owned by Bloem.
type StateService interface {
	Progress(ctx context.Context, subject Subject, itemID string) (Progress, error)
	SetProgress(ctx context.Context, subject Subject, itemID string, p ProgressUpdate) (Progress, error)
	SetWatched(ctx context.Context, subject Subject, itemID string, watched bool) error
	Favorites(ctx context.Context, subject Subject, cursor string, limit int) (ItemPage, error)
	SetFavorite(ctx context.Context, subject Subject, itemID string, favorite bool) error
	Bookmarks(ctx context.Context, subject Subject, itemID string) ([]Bookmark, error)
	CreateBookmark(ctx context.Context, subject Subject, b BookmarkCreate) (Bookmark, error)
	DeleteBookmark(ctx context.Context, subject Subject, bookmarkID string) error
	Collections(ctx context.Context, subject Subject, cursor string, limit int) (CollectionPage, error)
	Collection(ctx context.Context, subject Subject, collectionID string) (Collection, error)
	Playlists(ctx context.Context, subject Subject, cursor string, limit int) (PlaylistPage, error)
	CreatePlaylist(ctx context.Context, subject Subject, p PlaylistWrite) (Playlist, error)
	UpdatePlaylist(ctx context.Context, subject Subject, playlistID string, p PlaylistWrite) (Playlist, error)
	DeletePlaylist(ctx context.Context, subject Subject, playlistID string) error
	Downloads(ctx context.Context, subject Subject) ([]Download, error)
	CreateDownload(ctx context.Context, subject Subject, itemID string) (Download, error)
	DeleteDownload(ctx context.Context, subject Subject, downloadID string) error
}

// --- playback --------------------------------------------------------------

// PlaybackPlanRequest describes what the device can play.
type PlaybackPlanRequest struct {
	ItemID      string   `json:"item_id"`
	MaxBitrate  int64    `json:"max_bitrate,omitempty"`
	Containers  []string `json:"containers,omitempty"`
	VideoCodecs []string `json:"video_codecs,omitempty"`
	AudioCodecs []string `json:"audio_codecs,omitempty"`
}

// Plan is the server's playback decision.
type Plan struct {
	PlanID     string   `json:"plan_id"`
	Mode       string   `json:"mode"`
	Container  string   `json:"container,omitempty"`
	VideoCodec string   `json:"video_codec,omitempty"`
	AudioCodec string   `json:"audio_codec,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
}

// PlaybackStart begins a session.
type PlaybackStart struct {
	ItemID          string  `json:"item_id"`
	PlanID          string  `json:"plan_id,omitempty"`
	PositionSeconds float64 `json:"position_seconds,omitempty"`
}

// PlaybackSession is live session state; its grant is audience-bound.
type PlaybackSession struct {
	SessionID       string         `json:"session_id"`
	ItemID          string         `json:"item_id"`
	State           string         `json:"state"`
	PositionSeconds float64        `json:"position_seconds,omitempty"`
	Grant           *DeliveryGrant `json:"grant,omitempty"`
}

// PlaybackProgress reports session progress.
type PlaybackProgress struct {
	PositionSeconds float64 `json:"position_seconds"`
	State           string  `json:"state,omitempty"`
}

// PlaybackService plans, authorizes, and tracks playback. Grants are minted
// for the named audience.
type PlaybackService interface {
	Plan(ctx context.Context, subject Subject, req PlaybackPlanRequest) (Plan, error)
	Start(ctx context.Context, subject Subject, audience string, req PlaybackStart) (PlaybackSession, error)
	Session(ctx context.Context, subject Subject, sessionID string) (PlaybackSession, error)
	Sessions(ctx context.Context, subject Subject) ([]PlaybackSession, error)
	Progress(ctx context.Context, subject Subject, sessionID string, p PlaybackProgress) error
	Stop(ctx context.Context, subject Subject, sessionID string) error
}

// --- livetv ----------------------------------------------------------------

// Channel is a policy-visible Live TV channel.
type Channel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number,omitempty"`
}

// ChannelPage is one page of channels.
type ChannelPage struct {
	Channels   []Channel `json:"channels"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// GuideQuery selects a guide window.
type GuideQuery struct {
	ChannelID string
	Start     time.Time
	End       time.Time
	Cursor    string
	Limit     int
}

// GuideEntry is one program in the guide.
type GuideEntry struct {
	ChannelID string    `json:"channel_id"`
	Title     string    `json:"title"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
}

// GuidePage is one page of guide entries.
type GuidePage struct {
	Entries    []GuideEntry `json:"entries"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// Tuner reports availability only; tuner credentials never cross this API.
type Tuner struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// DVRRule is a recording rule.
type DVRRule struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	ChannelID string `json:"channel_id,omitempty"`
	ItemID    string `json:"item_id,omitempty"`
}

// DVRRuleCreate creates a recording rule.
type DVRRuleCreate struct {
	Kind      string `json:"kind"`
	ChannelID string `json:"channel_id,omitempty"`
	ItemID    string `json:"item_id,omitempty"`
}

// Recording is a completed or in-progress recording.
type Recording struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	State     string         `json:"state"`
	ChannelID string         `json:"channel_id,omitempty"`
	Start     time.Time      `json:"start,omitzero"`
	End       time.Time      `json:"end,omitzero"`
	Grant     *DeliveryGrant `json:"grant,omitempty"`
}

// RecordingPage is one page of recordings.
type RecordingPage struct {
	Recordings []Recording `json:"recordings"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// LiveTVService is the narrow Live TV surface.
type LiveTVService interface {
	Channels(ctx context.Context, subject Subject, cursor string, limit int) (ChannelPage, error)
	Guide(ctx context.Context, subject Subject, q GuideQuery) (GuidePage, error)
	Tuners(ctx context.Context, subject Subject) ([]Tuner, error)
	AuthorizeStream(ctx context.Context, subject Subject, audience, channelID string) (DeliveryGrant, error)
	DVRRules(ctx context.Context, subject Subject) ([]DVRRule, error)
	CreateDVRRule(ctx context.Context, subject Subject, rule DVRRuleCreate) (DVRRule, error)
	DeleteDVRRule(ctx context.Context, subject Subject, ruleID string) error
	Recordings(ctx context.Context, subject Subject, cursor string, limit int) (RecordingPage, error)
	Recording(ctx context.Context, subject Subject, recordingID string) (Recording, error)
	DeleteRecording(ctx context.Context, subject Subject, recordingID string) error
}

// --- events ----------------------------------------------------------------

// Event is one subject-visible event.
type Event struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	ItemID     string         `json:"item_id,omitempty"`
	OccurredAt time.Time      `json:"occurred_at"`
	Data       map[string]any `json:"data,omitempty"`
}

// EventBatch is one page of subject-filtered events. Resync signals a cursor
// gap requiring bounded resynchronization; NextCursor is then a fresh start.
type EventBatch struct {
	Events     []Event `json:"events"`
	NextCursor string  `json:"next_cursor"`
	Resync     bool    `json:"resync,omitempty"`
}

// EventsService serves subject-filtered events with resumable cursors.
type EventsService interface {
	Events(ctx context.Context, subject Subject, cursor string, limit int) (EventBatch, error)
}

// --- cross-cutting ---------------------------------------------------------

// RateLimiter is consulted per authenticated request. A nil limiter allows
// everything.
type RateLimiter interface {
	Allow(ctx context.Context, app AppIdentity, operationID string) bool
}

// IdempotencyStore persists mutation outcomes so replays are safe. The
// in-process default suffices for one server instance; a durable
// implementation can be wired in without changing handlers.
type IdempotencyStore interface {
	// Begin claims the (scope, key) pair for a request with the given
	// payload hash. It returns a stored response to replay, or claimed=true
	// when the caller must execute the operation, or ErrConflict when the
	// key was used with a different payload or is still executing.
	Begin(ctx context.Context, scope, key, requestHash string) (stored *StoredResponse, claimed bool, err error)
	// Complete records the outcome for future replays.
	Complete(ctx context.Context, scope, key string, response StoredResponse) error
	// Abandon releases a claim whose execution failed before producing a
	// storable response.
	Abandon(ctx context.Context, scope, key string) error
}

// StoredResponse is a replayable mutation outcome.
type StoredResponse struct {
	Status      int
	ContentType string
	Body        []byte
}

// Operation describes one registered route for contract-parity verification
// and administrative introspection.
type Operation struct {
	ID            string
	Method        string
	Path          string
	Capability    string
	Mutates       bool
	SubjectScoped bool
}
