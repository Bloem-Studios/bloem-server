// Package client is the generated-style Go client for the private
// Compatibility Service API v1 (contracts/compat/v1/openapi.yaml).
//
// One method exists per contract operation, grouped the way the contract
// groups them: enrollment/health, credential exchange/device/PIN/profile,
// catalog/search/detail/artwork, state/collections/playlists/downloads,
// playback, sessions, Live TV, and resumable events. Mutating calls send an
// Idempotency-Key automatically; subject-scoped calls take the subject token
// explicitly so one client can serve many end-user sessions.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/compatapi"
)

// Client calls the private compatibility API.
type Client struct {
	// BaseURL is the mount point, e.g.
	// http://vondel:8080/api/internal/compat/v1.
	BaseURL string
	// HTTPClient defaults to a client with a 30 second timeout.
	HTTPClient *http.Client
	// ServiceToken is the enrolled application's bearer credential.
	ServiceToken string
	// IdempotencyKeys generates keys for mutating calls. Defaults to
	// crypto/rand values; injectable for deterministic tests and replays.
	IdempotencyKeys func() string
}

// Option configures a Client.
type Option func(*Client)

// WithServiceToken sets the service bearer credential.
func WithServiceToken(token string) Option {
	return func(c *Client) { c.ServiceToken = token }
}

// WithHTTPClient replaces the underlying HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.HTTPClient = hc }
}

// New builds a client for the given base URL.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		BaseURL:         strings.TrimRight(baseURL, "/"),
		HTTPClient:      &http.Client{Timeout: 30 * time.Second},
		IdempotencyKeys: randomIdempotencyKey,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func randomIdempotencyKey() string {
	var buf [18]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(fmt.Sprintf("compatapi client: idempotency key entropy: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(buf[:])
}

// APIError is a decoded stable error envelope.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	TraceID    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("compat api: %d %s: %s (trace %s)", e.StatusCode, e.Code, e.Message, e.TraceID)
}

type callSpec struct {
	method       string
	path         string
	query        url.Values
	subjectToken string
	body         any
	mutates      bool
	noAuth       bool
}

func (c *Client) call(ctx context.Context, spec callSpec, out any) error {
	var reader io.Reader
	if spec.body != nil {
		encoded, err := json.Marshal(spec.body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	target := c.BaseURL + spec.path
	if len(spec.query) > 0 {
		target += "?" + spec.query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, spec.method, target, reader)
	if err != nil {
		return err
	}
	if spec.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !spec.noAuth && c.ServiceToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.ServiceToken)
	}
	if spec.subjectToken != "" {
		req.Header.Set("X-Vondel-Subject-Token", spec.subjectToken)
	}
	if spec.mutates {
		req.Header.Set("Idempotency-Key", c.IdempotencyKeys())
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				TraceID string `json:"trace_id"`
			} `json:"error"`
		}
		if json.Unmarshal(payload, &envelope) == nil {
			apiErr.Code = envelope.Error.Code
			apiErr.Message = envelope.Error.Message
			apiErr.TraceID = envelope.Error.TraceID
		}
		return apiErr
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func pageQuery(cursor string, limit int) url.Values {
	query := url.Values{}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	return query
}

// --- service ---------------------------------------------------------------

// EnrollRequest exchanges a one-use enrollment secret. Kind is a declaration
// verified against the secret's authoritative kind; ImageDigest is the
// running companion image, surfaced to administrators.
type EnrollRequest struct {
	Secret                string             `json:"secret"`
	Kind                  string             `json:"kind"`
	InstanceID            string             `json:"instance_id"`
	Version               string             `json:"version,omitempty"`
	ImageDigest           string             `json:"image_digest,omitempty"`
	API                   compatapi.APIRange `json:"api"`
	RequestedCapabilities []string           `json:"requested_capabilities,omitempty"`
}

// Enroll registers this application instance. The returned credential's raw
// token appears exactly once; store it, then call WithServiceToken.
func (c *Client) Enroll(ctx context.Context, req EnrollRequest) (compatapi.ServiceCredential, error) {
	var out compatapi.ServiceCredential
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/enroll", body: req, mutates: true, noAuth: true}, &out)
	return out, err
}

// RenewCredential exchanges the current service credential for a fresh one.
func (c *Client) RenewCredential(ctx context.Context) (compatapi.ServiceCredential, error) {
	var out compatapi.ServiceCredential
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/credentials/renew", mutates: true}, &out)
	return out, err
}

// Health reports server health, the API range, and this application's
// granted capabilities. Calling it records a heartbeat.
type Health struct {
	Status      string             `json:"status"`
	Version     string             `json:"version"`
	API         compatapi.APIRange `json:"api"`
	Application struct {
		ApplicationID string   `json:"application_id"`
		Kind          string   `json:"kind"`
		InstanceID    string   `json:"instance_id"`
		Capabilities  []string `json:"capabilities"`
	} `json:"application"`
}

// Health fetches /health, reporting the companion's own health status when
// one is given. An empty status is a contact-only heartbeat, recorded by the
// server as unknown; valid values are healthy, degraded, and unhealthy.
func (c *Client) Health(ctx context.Context, status string) (Health, error) {
	query := url.Values{}
	if status != "" {
		query.Set("status", status)
	}
	var out Health
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/health", query: query}, &out)
	return out, err
}

// --- identity --------------------------------------------------------------

// LoginRequest exchanges an end-user credential for a subject token.
type LoginRequest struct {
	GrantType string                `json:"grant_type"`
	Email     string                `json:"email,omitempty"`
	Username  string                `json:"username,omitempty"`
	Password  string                `json:"password"`
	Device    compatapi.DeviceClaim `json:"device"`
}

// SubjectFacts mirrors the contract's Subject schema.
type SubjectFacts struct {
	AccountID          int                   `json:"account_id"`
	ProfileID          string                `json:"profile_id"`
	OrganizationID     string                `json:"organization_id"`
	MembershipID       string                `json:"membership_id"`
	PolicyRevision     int64                 `json:"policy_revision"`
	SecurityRevision   int64                 `json:"security_revision"`
	CredentialRevision int64                 `json:"credential_revision"`
	AuthMethod         string                `json:"auth_method"`
	Device             compatapi.DeviceClaim `json:"device"`
}

// LoginResult carries the audience-bound subject token and its facts.
type LoginResult struct {
	SubjectToken string       `json:"subject_token"`
	ExpiresAt    time.Time    `json:"expires_at"`
	Subject      SubjectFacts `json:"subject"`
}

// Login exchanges an end-user credential.
func (c *Client) Login(ctx context.Context, req LoginRequest) (LoginResult, error) {
	var out LoginResult
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/identity/login", body: req, mutates: true}, &out)
	return out, err
}

// SubjectIntrospection is the current subject state.
type SubjectIntrospection struct {
	Subject       SubjectFacts `json:"subject"`
	ApplicationID string       `json:"application_id"`
	ExpiresAt     time.Time    `json:"expires_at"`
}

// IntrospectSubject revalidates and returns the subject behind a token.
func (c *Client) IntrospectSubject(ctx context.Context, subjectToken string) (SubjectIntrospection, error) {
	var out SubjectIntrospection
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/identity/subject", subjectToken: subjectToken}, &out)
	return out, err
}

// DeviceProfiles lists profile tiles authorized for the subject's device.
func (c *Client) DeviceProfiles(ctx context.Context, subjectToken string) ([]compatapi.ProfileTile, error) {
	var out struct {
		Profiles []compatapi.ProfileTile `json:"profiles"`
	}
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/identity/profiles", subjectToken: subjectToken}, &out)
	return out.Profiles, err
}

// profileTarget names a profile in PIN verification and switching bodies.
type profileTarget struct {
	ProfileID string `json:"profile_id"`
	PIN       string `json:"pin,omitempty"`
}

// VerifyPIN checks a profile switching PIN.
func (c *Client) VerifyPIN(ctx context.Context, subjectToken, profileID, pin string) error {
	body := profileTarget{ProfileID: profileID, PIN: pin}
	return c.call(ctx, callSpec{method: http.MethodPost, path: "/identity/pin/verify", subjectToken: subjectToken, body: body, mutates: true}, nil)
}

// SwitchProfile switches to another authorized profile, minting a new token.
func (c *Client) SwitchProfile(ctx context.Context, subjectToken, profileID, pin string) (LoginResult, error) {
	var out LoginResult
	body := profileTarget{ProfileID: profileID, PIN: pin}
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/identity/switch", subjectToken: subjectToken, body: body, mutates: true}, &out)
	return out, err
}

// Logout ends the subject's session.
func (c *Client) Logout(ctx context.Context, subjectToken string) error {
	return c.call(ctx, callSpec{method: http.MethodPost, path: "/identity/logout", subjectToken: subjectToken, mutates: true}, nil)
}

// --- catalog ---------------------------------------------------------------

// Libraries lists the subject's visible libraries.
func (c *Client) Libraries(ctx context.Context, subjectToken string) ([]compatapi.Library, error) {
	var out struct {
		Libraries []compatapi.Library `json:"libraries"`
	}
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/catalog/libraries", subjectToken: subjectToken}, &out)
	return out.Libraries, err
}

// ItemsQuery filters a catalog browse.
type ItemsQuery struct {
	LibraryID string
	Kind      string
	Sort      string
	Cursor    string
	Limit     int
}

// Items browses the catalog.
func (c *Client) Items(ctx context.Context, subjectToken string, q ItemsQuery) (compatapi.ItemPage, error) {
	query := pageQuery(q.Cursor, q.Limit)
	if q.LibraryID != "" {
		query.Set("library_id", q.LibraryID)
	}
	if q.Kind != "" {
		query.Set("kind", q.Kind)
	}
	if q.Sort != "" {
		query.Set("sort", q.Sort)
	}
	var out compatapi.ItemPage
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/catalog/items", query: query, subjectToken: subjectToken}, &out)
	return out, err
}

// Item fetches item detail.
func (c *Client) Item(ctx context.Context, subjectToken, itemID string) (compatapi.Item, error) {
	var out compatapi.Item
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/catalog/items/" + url.PathEscape(itemID), subjectToken: subjectToken}, &out)
	return out, err
}

// ItemChildren pages an item's children.
func (c *Client) ItemChildren(ctx context.Context, subjectToken, itemID, cursor string, limit int) (compatapi.ItemPage, error) {
	var out compatapi.ItemPage
	err := c.call(ctx, callSpec{
		method: http.MethodGet, path: "/catalog/items/" + url.PathEscape(itemID) + "/children",
		query: pageQuery(cursor, limit), subjectToken: subjectToken,
	}, &out)
	return out, err
}

// Search searches the catalog.
func (c *Client) Search(ctx context.Context, subjectToken, q, cursor string, limit int) (compatapi.ItemPage, error) {
	query := pageQuery(cursor, limit)
	query.Set("q", q)
	var out compatapi.ItemPage
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/catalog/search", query: query, subjectToken: subjectToken}, &out)
	return out, err
}

// Person fetches person detail.
func (c *Client) Person(ctx context.Context, subjectToken, personID string) (compatapi.Person, error) {
	var out compatapi.Person
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/catalog/people/" + url.PathEscape(personID), subjectToken: subjectToken}, &out)
	return out, err
}

// ArtworkGrant resolves artwork to an audience-bound delivery grant.
func (c *Client) ArtworkGrant(ctx context.Context, subjectToken, itemID, kind string) (compatapi.DeliveryGrant, error) {
	var out compatapi.DeliveryGrant
	err := c.call(ctx, callSpec{
		method:       http.MethodGet,
		path:         "/catalog/items/" + url.PathEscape(itemID) + "/artwork/" + url.PathEscape(kind),
		subjectToken: subjectToken,
	}, &out)
	return out, err
}

// --- state -----------------------------------------------------------------

// GetProgress reads canonical progress.
func (c *Client) GetProgress(ctx context.Context, subjectToken, itemID string) (compatapi.Progress, error) {
	var out compatapi.Progress
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/state/progress/" + url.PathEscape(itemID), subjectToken: subjectToken}, &out)
	return out, err
}

// SetProgress writes progress.
func (c *Client) SetProgress(ctx context.Context, subjectToken, itemID string, update compatapi.ProgressUpdate) (compatapi.Progress, error) {
	var out compatapi.Progress
	err := c.call(ctx, callSpec{method: http.MethodPut, path: "/state/progress/" + url.PathEscape(itemID), subjectToken: subjectToken, body: update, mutates: true}, &out)
	return out, err
}

// SetWatched marks an item watched or unwatched.
func (c *Client) SetWatched(ctx context.Context, subjectToken, itemID string, watched bool) error {
	body := map[string]bool{"watched": watched}
	return c.call(ctx, callSpec{method: http.MethodPut, path: "/state/watched/" + url.PathEscape(itemID), subjectToken: subjectToken, body: body, mutates: true}, nil)
}

// Favorites pages the subject's favorites.
func (c *Client) Favorites(ctx context.Context, subjectToken, cursor string, limit int) (compatapi.ItemPage, error) {
	var out compatapi.ItemPage
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/state/favorites", query: pageQuery(cursor, limit), subjectToken: subjectToken}, &out)
	return out, err
}

// SetFavorite sets or clears a favorite flag.
func (c *Client) SetFavorite(ctx context.Context, subjectToken, itemID string, favorite bool) error {
	body := map[string]bool{"favorite": favorite}
	return c.call(ctx, callSpec{method: http.MethodPut, path: "/state/favorites/" + url.PathEscape(itemID), subjectToken: subjectToken, body: body, mutates: true}, nil)
}

// Bookmarks lists bookmarks, optionally for one item.
func (c *Client) Bookmarks(ctx context.Context, subjectToken, itemID string) ([]compatapi.Bookmark, error) {
	query := url.Values{}
	if itemID != "" {
		query.Set("item_id", itemID)
	}
	var out struct {
		Bookmarks []compatapi.Bookmark `json:"bookmarks"`
	}
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/state/bookmarks", query: query, subjectToken: subjectToken}, &out)
	return out.Bookmarks, err
}

// CreateBookmark creates a bookmark.
func (c *Client) CreateBookmark(ctx context.Context, subjectToken string, create compatapi.BookmarkCreate) (compatapi.Bookmark, error) {
	var out compatapi.Bookmark
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/state/bookmarks", subjectToken: subjectToken, body: create, mutates: true}, &out)
	return out, err
}

// DeleteBookmark deletes a bookmark.
func (c *Client) DeleteBookmark(ctx context.Context, subjectToken, bookmarkID string) error {
	return c.call(ctx, callSpec{method: http.MethodDelete, path: "/state/bookmarks/" + url.PathEscape(bookmarkID), subjectToken: subjectToken, mutates: true}, nil)
}

// CollectionPage mirrors the contract's collection page.
type CollectionPage struct {
	Collections []compatapi.Collection `json:"collections"`
	NextCursor  string                 `json:"next_cursor"`
}

// Collections pages the subject's visible collections.
func (c *Client) Collections(ctx context.Context, subjectToken, cursor string, limit int) (CollectionPage, error) {
	var out CollectionPage
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/state/collections", query: pageQuery(cursor, limit), subjectToken: subjectToken}, &out)
	return out, err
}

// Collection fetches collection detail.
func (c *Client) Collection(ctx context.Context, subjectToken, collectionID string) (compatapi.Collection, error) {
	var out compatapi.Collection
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/state/collections/" + url.PathEscape(collectionID), subjectToken: subjectToken}, &out)
	return out, err
}

// PlaylistPage mirrors the contract's playlist page.
type PlaylistPage struct {
	Playlists  []compatapi.Playlist `json:"playlists"`
	NextCursor string               `json:"next_cursor"`
}

// Playlists pages the subject's playlists.
func (c *Client) Playlists(ctx context.Context, subjectToken, cursor string, limit int) (PlaylistPage, error) {
	var out PlaylistPage
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/state/playlists", query: pageQuery(cursor, limit), subjectToken: subjectToken}, &out)
	return out, err
}

// CreatePlaylist creates a playlist.
func (c *Client) CreatePlaylist(ctx context.Context, subjectToken string, write compatapi.PlaylistWrite) (compatapi.Playlist, error) {
	var out compatapi.Playlist
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/state/playlists", subjectToken: subjectToken, body: write, mutates: true}, &out)
	return out, err
}

// UpdatePlaylist replaces a playlist's name and items.
func (c *Client) UpdatePlaylist(ctx context.Context, subjectToken, playlistID string, write compatapi.PlaylistWrite) (compatapi.Playlist, error) {
	var out compatapi.Playlist
	err := c.call(ctx, callSpec{method: http.MethodPut, path: "/state/playlists/" + url.PathEscape(playlistID), subjectToken: subjectToken, body: write, mutates: true}, &out)
	return out, err
}

// DeletePlaylist deletes a playlist.
func (c *Client) DeletePlaylist(ctx context.Context, subjectToken, playlistID string) error {
	return c.call(ctx, callSpec{method: http.MethodDelete, path: "/state/playlists/" + url.PathEscape(playlistID), subjectToken: subjectToken, mutates: true}, nil)
}

// Downloads lists the subject's downloads on the bound device.
func (c *Client) Downloads(ctx context.Context, subjectToken string) ([]compatapi.Download, error) {
	var out struct {
		Downloads []compatapi.Download `json:"downloads"`
	}
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/state/downloads", subjectToken: subjectToken}, &out)
	return out.Downloads, err
}

// CreateDownload requests a download for offline use.
func (c *Client) CreateDownload(ctx context.Context, subjectToken, itemID string) (compatapi.Download, error) {
	body := map[string]string{"item_id": itemID}
	var out compatapi.Download
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/state/downloads", subjectToken: subjectToken, body: body, mutates: true}, &out)
	return out, err
}

// DeleteDownload removes a download.
func (c *Client) DeleteDownload(ctx context.Context, subjectToken, downloadID string) error {
	return c.call(ctx, callSpec{method: http.MethodDelete, path: "/state/downloads/" + url.PathEscape(downloadID), subjectToken: subjectToken, mutates: true}, nil)
}

// --- playback --------------------------------------------------------------

// PlanPlayback plans playback for an item and device capabilities.
func (c *Client) PlanPlayback(ctx context.Context, subjectToken string, req compatapi.PlaybackPlanRequest) (compatapi.Plan, error) {
	var out compatapi.Plan
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/playback/plan", subjectToken: subjectToken, body: req, mutates: true}, &out)
	return out, err
}

// StartSession starts playback and receives a delivery grant.
func (c *Client) StartSession(ctx context.Context, subjectToken string, req compatapi.PlaybackStart) (compatapi.PlaybackSession, error) {
	var out compatapi.PlaybackSession
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/playback/sessions", subjectToken: subjectToken, body: req, mutates: true}, &out)
	return out, err
}

// Sessions lists the subject's live playback sessions.
func (c *Client) Sessions(ctx context.Context, subjectToken string) ([]compatapi.PlaybackSession, error) {
	var out struct {
		Sessions []compatapi.PlaybackSession `json:"sessions"`
	}
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/playback/sessions", subjectToken: subjectToken}, &out)
	return out.Sessions, err
}

// Session reads one session's state, e.g. for recovery after restart.
func (c *Client) Session(ctx context.Context, subjectToken, sessionID string) (compatapi.PlaybackSession, error) {
	var out compatapi.PlaybackSession
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/playback/sessions/" + url.PathEscape(sessionID), subjectToken: subjectToken}, &out)
	return out, err
}

// ReportProgress reports playback progress for a session.
func (c *Client) ReportProgress(ctx context.Context, subjectToken, sessionID string, progress compatapi.PlaybackProgress) error {
	return c.call(ctx, callSpec{
		method: http.MethodPost, path: "/playback/sessions/" + url.PathEscape(sessionID) + "/progress",
		subjectToken: subjectToken, body: progress, mutates: true,
	}, nil)
}

// StopSession stops a playback session.
func (c *Client) StopSession(ctx context.Context, subjectToken, sessionID string) error {
	return c.call(ctx, callSpec{
		method: http.MethodPost, path: "/playback/sessions/" + url.PathEscape(sessionID) + "/stop",
		subjectToken: subjectToken, mutates: true,
	}, nil)
}

// --- livetv ----------------------------------------------------------------

// ChannelPage mirrors the contract's channel page.
type ChannelPage struct {
	Channels   []compatapi.Channel `json:"channels"`
	NextCursor string              `json:"next_cursor"`
}

// LiveTVChannels pages the visible channels.
func (c *Client) LiveTVChannels(ctx context.Context, subjectToken, cursor string, limit int) (ChannelPage, error) {
	var out ChannelPage
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/livetv/channels", query: pageQuery(cursor, limit), subjectToken: subjectToken}, &out)
	return out, err
}

// GuideQuery selects a guide window.
type GuideQuery struct {
	ChannelID string
	Start     time.Time
	End       time.Time
	Cursor    string
	Limit     int
}

// GuidePage mirrors the contract's guide page.
type GuidePage struct {
	Entries    []compatapi.GuideEntry `json:"entries"`
	NextCursor string                 `json:"next_cursor"`
}

// LiveTVGuide pages guide entries for a window.
func (c *Client) LiveTVGuide(ctx context.Context, subjectToken string, q GuideQuery) (GuidePage, error) {
	query := pageQuery(q.Cursor, q.Limit)
	if q.ChannelID != "" {
		query.Set("channel_id", q.ChannelID)
	}
	if !q.Start.IsZero() {
		query.Set("start", q.Start.Format(time.RFC3339))
	}
	if !q.End.IsZero() {
		query.Set("end", q.End.Format(time.RFC3339))
	}
	var out GuidePage
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/livetv/guide", query: query, subjectToken: subjectToken}, &out)
	return out, err
}

// LiveTVTuners reports tuner availability.
func (c *Client) LiveTVTuners(ctx context.Context, subjectToken string) ([]compatapi.Tuner, error) {
	var out struct {
		Tuners []compatapi.Tuner `json:"tuners"`
	}
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/livetv/tuners", subjectToken: subjectToken}, &out)
	return out.Tuners, err
}

// AuthorizeLiveStream authorizes a live stream and returns its grant.
func (c *Client) AuthorizeLiveStream(ctx context.Context, subjectToken, channelID string) (compatapi.DeliveryGrant, error) {
	body := map[string]string{"channel_id": channelID}
	var out compatapi.DeliveryGrant
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/livetv/streams", subjectToken: subjectToken, body: body, mutates: true}, &out)
	return out, err
}

// DVRRules lists recording rules.
func (c *Client) DVRRules(ctx context.Context, subjectToken string) ([]compatapi.DVRRule, error) {
	var out struct {
		Rules []compatapi.DVRRule `json:"rules"`
	}
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/livetv/dvr/rules", subjectToken: subjectToken}, &out)
	return out.Rules, err
}

// CreateDVRRule creates a recording rule.
func (c *Client) CreateDVRRule(ctx context.Context, subjectToken string, create compatapi.DVRRuleCreate) (compatapi.DVRRule, error) {
	var out compatapi.DVRRule
	err := c.call(ctx, callSpec{method: http.MethodPost, path: "/livetv/dvr/rules", subjectToken: subjectToken, body: create, mutates: true}, &out)
	return out, err
}

// DeleteDVRRule deletes a recording rule.
func (c *Client) DeleteDVRRule(ctx context.Context, subjectToken, ruleID string) error {
	return c.call(ctx, callSpec{method: http.MethodDelete, path: "/livetv/dvr/rules/" + url.PathEscape(ruleID), subjectToken: subjectToken, mutates: true}, nil)
}

// RecordingPage mirrors the contract's recording page.
type RecordingPage struct {
	Recordings []compatapi.Recording `json:"recordings"`
	NextCursor string                `json:"next_cursor"`
}

// Recordings pages the visible recordings.
func (c *Client) Recordings(ctx context.Context, subjectToken, cursor string, limit int) (RecordingPage, error) {
	var out RecordingPage
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/livetv/recordings", query: pageQuery(cursor, limit), subjectToken: subjectToken}, &out)
	return out, err
}

// Recording fetches recording detail.
func (c *Client) Recording(ctx context.Context, subjectToken, recordingID string) (compatapi.Recording, error) {
	var out compatapi.Recording
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/livetv/recordings/" + url.PathEscape(recordingID), subjectToken: subjectToken}, &out)
	return out, err
}

// DeleteRecording deletes a recording.
func (c *Client) DeleteRecording(ctx context.Context, subjectToken, recordingID string) error {
	return c.call(ctx, callSpec{method: http.MethodDelete, path: "/livetv/recordings/" + url.PathEscape(recordingID), subjectToken: subjectToken, mutates: true}, nil)
}

// --- events ----------------------------------------------------------------

// EventBatch mirrors the contract's event batch.
type EventBatch struct {
	Events     []compatapi.Event `json:"events"`
	NextCursor string            `json:"next_cursor"`
	Resync     bool              `json:"resync"`
}

// Events fetches one batch of subject-filtered events. Pass the previous
// batch's NextCursor to resume; a Resync response requires a bounded
// resynchronization from the fresh cursor.
func (c *Client) Events(ctx context.Context, subjectToken, cursor string, limit int) (EventBatch, error) {
	var out EventBatch
	err := c.call(ctx, callSpec{method: http.MethodGet, path: "/events", query: pageQuery(cursor, limit), subjectToken: subjectToken}, &out)
	return out, err
}
