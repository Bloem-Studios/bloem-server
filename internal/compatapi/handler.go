package compatapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	// subjectTokenHeader carries the audience-bound subject token; it is the
	// only channel through which a subject is ever named.
	subjectTokenHeader = "X-Vondel-Subject-Token"
	idempotencyHeader  = "Idempotency-Key"
	traceHeader        = "X-Trace-Id"

	defaultSubjectTokenTTL = 15 * time.Minute
	defaultIdempotencyTTL  = 6 * time.Hour
	maxBodyBytes           = 1 << 20 // 1 MiB
	defaultPageLimit       = 100
	maxPageLimit           = 500
)

// Config carries the handler's static configuration.
type Config struct {
	// SubjectTokenKey signs subject tokens. Minimum 32 bytes.
	SubjectTokenKey []byte
	// CursorKey signs list cursors. Minimum 32 bytes.
	CursorKey []byte
	// SubjectTokenTTL bounds subject token lifetime; defaults to 15 minutes.
	SubjectTokenTTL time.Duration
	// Version is reported by /health.
	Version string
	// APIRange is the API compatibility range reported by /health.
	APIRange APIRange
	// Now is the clock; defaults to time.Now. Injected for tests.
	Now func() time.Time
}

// Services carries the narrow dependencies handlers adapt. Apps is required;
// every other dependency may be nil, in which case its operations fail closed
// with 503 unavailable.
type Services struct {
	Apps        AppAuthenticator
	Enroller    AppEnroller
	Renewer     CredentialRenewer
	Heartbeats  HeartbeatRecorder
	Subjects    SubjectService
	Catalog     CatalogService
	State       StateService
	Playback    PlaybackService
	LiveTV      LiveTVService
	Events      EventsService
	Idempotency IdempotencyStore
	RateLimiter RateLimiter
}

// Handler serves the private Compatibility Service API v1. It implements
// http.Handler with paths relative to its mount point
// (/api/internal/compat/v1).
type Handler struct {
	cfg        Config
	svc        Services
	mux        chi.Router
	operations []Operation
}

// New builds the handler, failing closed on weak or missing configuration.
func New(cfg Config, svc Services) (*Handler, error) {
	if svc.Apps == nil {
		return nil, errors.New("compatapi: an application authenticator is required")
	}
	if len(cfg.SubjectTokenKey) < 32 {
		return nil, errors.New("compatapi: subject token key must be at least 32 bytes")
	}
	if len(cfg.CursorKey) < 32 {
		return nil, errors.New("compatapi: cursor key must be at least 32 bytes")
	}
	if cfg.SubjectTokenTTL <= 0 {
		cfg.SubjectTokenTTL = defaultSubjectTokenTTL
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if svc.Idempotency == nil {
		svc.Idempotency = newMemoryIdempotencyStore(cfg.Now, defaultIdempotencyTTL)
	}
	h := &Handler{cfg: cfg, svc: svc}
	h.buildRoutes()
	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// Operations returns every registered operation for contract-parity checks
// and administration.
func (h *Handler) Operations() []Operation {
	out := make([]Operation, len(h.operations))
	copy(out, h.operations)
	return out
}

// --- request context -------------------------------------------------------

type requestContext struct {
	app        AppIdentity
	subject    Subject
	tokenState subjectTokenClaims
}

type endpointFunc func(w http.ResponseWriter, r *http.Request, rc *requestContext)

// --- routing ---------------------------------------------------------------

// opEnroll is the one operation that authenticates with its body secret
// instead of a service bearer; the policy chain special-cases it by id.
const opEnroll = "enroll"

func (h *Handler) buildRoutes() {
	mux := chi.NewRouter()
	mux.Use(h.traceMiddleware)
	mux.NotFound(func(w http.ResponseWriter, r *http.Request) {
		h.writeError(w, r, http.StatusNotFound, "not_found", "resource not found")
	})
	mux.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		h.writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})

	type route struct {
		id         string
		method     string
		path       string
		capability string
		subject    bool
		fn         endpointFunc
	}
	routes := []route{
		// service
		{opEnroll, http.MethodPost, "/enroll", CapabilityService, false, h.handleEnroll},
		{"renewCredential", http.MethodPost, "/credentials/renew", CapabilityService, false, h.handleRenewCredential},
		{"health", http.MethodGet, "/health", CapabilityService, false, h.handleHealth},
		// identity
		{"login", http.MethodPost, "/identity/login", CapabilityIdentity, false, h.handleLogin},
		{"introspectSubject", http.MethodGet, "/identity/subject", CapabilityIdentity, true, h.handleIntrospectSubject},
		{"listDeviceProfiles", http.MethodGet, "/identity/profiles", CapabilityIdentity, true, h.handleListDeviceProfiles},
		{"verifyProfilePin", http.MethodPost, "/identity/pin/verify", CapabilityIdentity, true, h.handleVerifyProfilePin},
		{"switchProfile", http.MethodPost, "/identity/switch", CapabilityIdentity, true, h.handleSwitchProfile},
		{"logout", http.MethodPost, "/identity/logout", CapabilityIdentity, true, h.handleLogout},
		// catalog
		{"listLibraries", http.MethodGet, "/catalog/libraries", CapabilityCatalog, true, h.handleListLibraries},
		{"listItems", http.MethodGet, "/catalog/items", CapabilityCatalog, true, h.handleListItems},
		{"getItem", http.MethodGet, "/catalog/items/{itemID}", CapabilityCatalog, true, h.handleGetItem},
		{"listItemChildren", http.MethodGet, "/catalog/items/{itemID}/children", CapabilityCatalog, true, h.handleListItemChildren},
		{"searchItems", http.MethodGet, "/catalog/search", CapabilityCatalog, true, h.handleSearchItems},
		{"getPerson", http.MethodGet, "/catalog/people/{personID}", CapabilityCatalog, true, h.handleGetPerson},
		{"resolveArtwork", http.MethodGet, "/catalog/items/{itemID}/artwork/{artworkKind}", CapabilityCatalog, true, h.handleResolveArtwork},
		// state
		{"getProgress", http.MethodGet, "/state/progress/{itemID}", CapabilityState, true, h.handleGetProgress},
		{"setProgress", http.MethodPut, "/state/progress/{itemID}", CapabilityState, true, h.handleSetProgress},
		{"setWatched", http.MethodPut, "/state/watched/{itemID}", CapabilityState, true, h.handleSetWatched},
		{"listFavorites", http.MethodGet, "/state/favorites", CapabilityState, true, h.handleListFavorites},
		{"setFavorite", http.MethodPut, "/state/favorites/{itemID}", CapabilityState, true, h.handleSetFavorite},
		{"listBookmarks", http.MethodGet, "/state/bookmarks", CapabilityState, true, h.handleListBookmarks},
		{"createBookmark", http.MethodPost, "/state/bookmarks", CapabilityState, true, h.handleCreateBookmark},
		{"deleteBookmark", http.MethodDelete, "/state/bookmarks/{bookmarkID}", CapabilityState, true, h.handleDeleteBookmark},
		{"listCollections", http.MethodGet, "/state/collections", CapabilityState, true, h.handleListCollections},
		{"getCollection", http.MethodGet, "/state/collections/{collectionID}", CapabilityState, true, h.handleGetCollection},
		{"listPlaylists", http.MethodGet, "/state/playlists", CapabilityState, true, h.handleListPlaylists},
		{"createPlaylist", http.MethodPost, "/state/playlists", CapabilityState, true, h.handleCreatePlaylist},
		{"updatePlaylist", http.MethodPut, "/state/playlists/{playlistID}", CapabilityState, true, h.handleUpdatePlaylist},
		{"deletePlaylist", http.MethodDelete, "/state/playlists/{playlistID}", CapabilityState, true, h.handleDeletePlaylist},
		{"listDownloads", http.MethodGet, "/state/downloads", CapabilityState, true, h.handleListDownloads},
		{"createDownload", http.MethodPost, "/state/downloads", CapabilityState, true, h.handleCreateDownload},
		{"deleteDownload", http.MethodDelete, "/state/downloads/{downloadID}", CapabilityState, true, h.handleDeleteDownload},
		// playback
		{"planPlayback", http.MethodPost, "/playback/plan", CapabilityPlayback, true, h.handlePlanPlayback},
		{"startPlaybackSession", http.MethodPost, "/playback/sessions", CapabilityPlayback, true, h.handleStartPlaybackSession},
		{"listPlaybackSessions", http.MethodGet, "/playback/sessions", CapabilityPlayback, true, h.handleListPlaybackSessions},
		{"getPlaybackSession", http.MethodGet, "/playback/sessions/{sessionID}", CapabilityPlayback, true, h.handleGetPlaybackSession},
		{"reportPlaybackProgress", http.MethodPost, "/playback/sessions/{sessionID}/progress", CapabilityPlayback, true, h.handleReportPlaybackProgress},
		{"stopPlaybackSession", http.MethodPost, "/playback/sessions/{sessionID}/stop", CapabilityPlayback, true, h.handleStopPlaybackSession},
		// livetv
		{"listLiveTvChannels", http.MethodGet, "/livetv/channels", CapabilityLiveTV, true, h.handleListLiveTvChannels},
		{"getLiveTvGuide", http.MethodGet, "/livetv/guide", CapabilityLiveTV, true, h.handleGetLiveTvGuide},
		{"listLiveTvTuners", http.MethodGet, "/livetv/tuners", CapabilityLiveTV, true, h.handleListLiveTvTuners},
		{"authorizeLiveTvStream", http.MethodPost, "/livetv/streams", CapabilityLiveTV, true, h.handleAuthorizeLiveTvStream},
		{"listDvrRules", http.MethodGet, "/livetv/dvr/rules", CapabilityLiveTV, true, h.handleListDvrRules},
		{"createDvrRule", http.MethodPost, "/livetv/dvr/rules", CapabilityLiveTV, true, h.handleCreateDvrRule},
		{"deleteDvrRule", http.MethodDelete, "/livetv/dvr/rules/{ruleID}", CapabilityLiveTV, true, h.handleDeleteDvrRule},
		{"listRecordings", http.MethodGet, "/livetv/recordings", CapabilityLiveTV, true, h.handleListRecordings},
		{"getRecording", http.MethodGet, "/livetv/recordings/{recordingID}", CapabilityLiveTV, true, h.handleGetRecording},
		{"deleteRecording", http.MethodDelete, "/livetv/recordings/{recordingID}", CapabilityLiveTV, true, h.handleDeleteRecording},
		// events
		{"listEvents", http.MethodGet, "/events", CapabilityEvents, true, h.handleListEvents},
	}

	for _, rt := range routes {
		op := Operation{
			ID:            rt.id,
			Method:        rt.method,
			Path:          rt.path,
			Capability:    rt.capability,
			Mutates:       rt.method != http.MethodGet && rt.method != http.MethodHead,
			SubjectScoped: rt.subject,
		}
		h.operations = append(h.operations, op)
		mux.Method(rt.method, rt.path, h.endpoint(op, rt.fn))
	}
	h.mux = mux
}

// endpoint wraps a handler with the full policy chain: application
// authentication, rate limiting, capability enforcement, subject resolution
// and reauthorization, and idempotency. Order matters and is pinned by tests.
func (h *Handler) endpoint(op Operation, fn endpointFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		rc := &requestContext{}

		// 1. Application authentication. Enrollment authenticates with the
		// one-use secret in its body instead of a bearer.
		if op.ID != opEnroll {
			bearer, ok := bearerToken(r)
			if !ok {
				h.writeError(w, r, http.StatusUnauthorized, "unauthorized", "a service credential is required")
				return
			}
			app, err := h.svc.Apps.Authenticate(r.Context(), bearer, r.TLS)
			if err != nil {
				h.writeError(w, r, http.StatusUnauthorized, "unauthorized", "the service credential was refused")
				return
			}
			rc.app = app
		}

		// 2. Rate limiting.
		if h.svc.RateLimiter != nil && !h.svc.RateLimiter.Allow(r.Context(), rc.app, op.ID) {
			h.writeError(w, r, http.StatusTooManyRequests, "rate_limited", "request budget exceeded")
			return
		}

		// 3. Capability enforcement.
		if op.ID != opEnroll && !rc.app.HasCapability(op.Capability) {
			h.writeError(w, r, http.StatusForbidden, "capability_missing", "capability not granted")
			return
		}

		// 4. Subject resolution and reauthorization.
		if op.SubjectScoped {
			raw := r.Header.Get(subjectTokenHeader)
			if raw == "" {
				h.writeError(w, r, http.StatusUnauthorized, "subject_token_required", "a subject token is required")
				return
			}
			claims, err := h.verifySubjectToken(raw, rc.app.ApplicationID)
			if err != nil {
				h.writeError(w, r, http.StatusUnauthorized, "subject_token_invalid", "the subject token was refused")
				return
			}
			if h.svc.Subjects == nil {
				h.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "subject resolution is unavailable")
				return
			}
			// Bloem reauthorizes at its boundary: the token is only a
			// pointer to the subject, never the authority for it.
			current, err := h.svc.Subjects.CurrentSubject(r.Context(), claims.Subject.subject())
			if err != nil {
				if errors.Is(err, ErrSubjectRevoked) {
					h.writeError(w, r, http.StatusUnauthorized, "subject_revoked", "the subject is no longer authorized")
					return
				}
				h.writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
				return
			}
			rc.subject = current
			rc.tokenState = claims
		}

		// 5. Idempotency for mutations.
		if op.Mutates {
			h.withIdempotency(w, r, op, rc, fn)
			return
		}
		fn(w, r, rc)
	}
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || len(header) == len(prefix) {
		return "", false
	}
	return header[len(prefix):], true
}

// --- tracing ---------------------------------------------------------------

type traceContextKey struct{}

func (h *Handler) traceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get(traceHeader)
		if traceID == "" {
			traceID = uuid.NewString()
		}
		w.Header().Set(traceHeader, traceID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), traceContextKey{}, traceID)))
	})
}

func traceID(r *http.Request) string {
	id, _ := r.Context().Value(traceContextKey{}).(string)
	return id
}

// --- responses -------------------------------------------------------------

func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type errorEnvelope struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id"`
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	h.writeJSON(w, status, errorEnvelope{Error: errorDetail{Code: code, Message: message, TraceID: traceID(r)}})
}

// writeServiceError maps a domain error onto the stable envelope. Messages
// are fixed strings: a domain error never travels to a companion verbatim, so
// identifiers, SQL, and driver detail cannot leak.
func (h *Handler) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		h.writeError(w, r, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, ErrUnavailable):
		h.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "the backing capability is unavailable")
	case errors.Is(err, ErrConflict):
		h.writeError(w, r, http.StatusConflict, "conflict", "the request conflicts with current state")
	case errors.Is(err, ErrInvalid):
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "the request is invalid")
	case errors.Is(err, ErrForbidden):
		h.writeError(w, r, http.StatusForbidden, "forbidden", "the operation is not permitted")
	case errors.Is(err, ErrInvalidCredentials):
		h.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "the credentials were refused")
	case errors.Is(err, ErrSubjectRevoked):
		h.writeError(w, r, http.StatusUnauthorized, "subject_revoked", "the subject is no longer authorized")
	case errors.Is(err, ErrPINInvalid):
		h.writeError(w, r, http.StatusForbidden, "pin_invalid", "the pin was refused")
	default:
		h.writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
	}
}

func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "the request body is not valid JSON")
		return false
	}
	return true
}

// checkGrant refuses a delivery grant minted for the wrong audience: that is
// a server-side bug and the grant must never leave the boundary.
func (h *Handler) checkGrant(w http.ResponseWriter, r *http.Request, app AppIdentity, grant *DeliveryGrant) bool {
	if grant == nil {
		return true
	}
	if grant.Audience != app.ApplicationID {
		h.writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
		return false
	}
	return true
}

// --- subject tokens --------------------------------------------------------

// subjectWire is the wire projection of Subject; it is also the payload
// carried inside signed subject tokens so all subject facts survive the
// round trip.
type subjectWire struct {
	AccountID          int         `json:"account_id"`
	ProfileID          string      `json:"profile_id"`
	OrganizationID     string      `json:"organization_id"`
	MembershipID       string      `json:"membership_id"`
	PolicyRevision     int64       `json:"policy_revision"`
	SecurityRevision   int64       `json:"security_revision"`
	CredentialRevision int64       `json:"credential_revision"`
	AuthMethod         string      `json:"auth_method"`
	Device             DeviceClaim `json:"device"`
}

func subjectToWire(s Subject) subjectWire {
	return subjectWire{
		AccountID:          s.AccountID,
		ProfileID:          s.ProfileID,
		OrganizationID:     s.OrganizationID,
		MembershipID:       s.MembershipID,
		PolicyRevision:     s.PolicyRevision,
		SecurityRevision:   s.SecurityRevision,
		CredentialRevision: s.CredentialRevision,
		AuthMethod:         s.AuthMethod,
		Device:             s.Device,
	}
}

func (w subjectWire) subject() Subject {
	return Subject{
		AccountID:          w.AccountID,
		ProfileID:          w.ProfileID,
		OrganizationID:     w.OrganizationID,
		MembershipID:       w.MembershipID,
		PolicyRevision:     w.PolicyRevision,
		SecurityRevision:   w.SecurityRevision,
		CredentialRevision: w.CredentialRevision,
		AuthMethod:         w.AuthMethod,
		Device:             w.Device,
	}
}

type subjectTokenClaims struct {
	Audience  string      `json:"aud"`
	IssuedAt  int64       `json:"iat"`
	ExpiresAt int64       `json:"exp"`
	Subject   subjectWire `json:"sub"`
}

var errTokenInvalid = errors.New("compatapi: subject token invalid")

// mintSubjectToken issues an audience-bound token for the subject. The token
// is a signed pointer, not a credential store: the subject is revalidated on
// every request.
func (h *Handler) mintSubjectToken(subject Subject, audience string) (string, time.Time) {
	now := h.cfg.Now()
	expires := now.Add(h.cfg.SubjectTokenTTL)
	claims := subjectTokenClaims{
		Audience:  audience,
		IssuedAt:  now.Unix(),
		ExpiresAt: expires.Unix(),
		Subject:   subjectToWire(subject),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		// Claims are plain data; marshaling cannot fail at runtime.
		panic(fmt.Sprintf("compatapi: marshal subject token: %v", err))
	}
	return signPayload(h.cfg.SubjectTokenKey, payload), expires
}

func (h *Handler) verifySubjectToken(raw, audience string) (subjectTokenClaims, error) {
	payload, err := verifyPayload(h.cfg.SubjectTokenKey, raw)
	if err != nil {
		return subjectTokenClaims{}, err
	}
	var claims subjectTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return subjectTokenClaims{}, errTokenInvalid
	}
	if claims.Audience != audience {
		return subjectTokenClaims{}, errTokenInvalid
	}
	if !h.cfg.Now().Before(time.Unix(claims.ExpiresAt, 0)) {
		return subjectTokenClaims{}, errTokenInvalid
	}
	return claims, nil
}

func signPayload(key, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyPayload(key []byte, raw string) ([]byte, error) {
	dot := strings.LastIndexByte(raw, '.')
	if dot < 0 {
		return nil, errTokenInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw[:dot])
	if err != nil {
		return nil, errTokenInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(raw[dot+1:])
	if err != nil {
		return nil, errTokenInvalid
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errTokenInvalid
	}
	return payload, nil
}

// --- signed cursors --------------------------------------------------------

type cursorClaims struct {
	Scope string `json:"scope"`
	Token string `json:"token"`
}

var errCursorInvalid = errors.New("compatapi: cursor invalid")

// encodeCursor signs a domain continuation token, scoped to one operation so
// a cursor cannot be replayed against a different list.
func (h *Handler) encodeCursor(scope, domainToken string) string {
	if domainToken == "" {
		return ""
	}
	payload, err := json.Marshal(cursorClaims{Scope: scope, Token: domainToken})
	if err != nil {
		panic(fmt.Sprintf("compatapi: marshal cursor: %v", err))
	}
	return signPayload(h.cfg.CursorKey, payload)
}

func (h *Handler) decodeCursor(scope, wire string) (string, error) {
	if wire == "" {
		return "", nil
	}
	payload, err := verifyPayload(h.cfg.CursorKey, wire)
	if err != nil {
		return "", errCursorInvalid
	}
	var claims cursorClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", errCursorInvalid
	}
	if claims.Scope != scope {
		return "", errCursorInvalid
	}
	return claims.Token, nil
}

// listPayload builds the common list response body: the named collection
// plus the signed continuation cursor for the operation.
func (h *Handler) listPayload(op, itemsKey string, items any, domainCursor string) map[string]any {
	return map[string]any{
		itemsKey:      items,
		"next_cursor": h.encodeCursor(op, domainCursor),
	}
}

// pageParams extracts and validates the signed cursor and bounded limit for
// a list operation. It writes the error response itself on failure.
func (h *Handler) pageParams(w http.ResponseWriter, r *http.Request, op Operation) (cursor string, limit int, ok bool) {
	cursor, err := h.decodeCursor(op.ID, r.URL.Query().Get("cursor"))
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_cursor", "the cursor was refused")
		return "", 0, false
	}
	limit = defaultPageLimit
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 {
			h.writeError(w, r, http.StatusBadRequest, "invalid_request", "limit must be a positive integer")
			return "", 0, false
		}
		limit = parsed
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	return cursor, limit, true
}

// --- idempotency -----------------------------------------------------------

func (h *Handler) withIdempotency(w http.ResponseWriter, r *http.Request, op Operation, rc *requestContext, fn endpointFunc) {
	key := r.Header.Get(idempotencyHeader)
	if key == "" {
		h.writeError(w, r, http.StatusBadRequest, "idempotency_key_required", "mutations require an Idempotency-Key header")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "the request body could not be read")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	scope := rc.app.ApplicationID + "\x00" + op.ID
	if op.ID == opEnroll {
		// Enrollment is unauthenticated, so the application id above is
		// empty and one shared scope would let any caller pre-poison another
		// companion's retry by spending its idempotency key first. Deriving
		// the scope from the presented secret keeps callers apart: without
		// the victim's secret an attacker cannot touch the victim's scope.
		var probe struct {
			Secret string `json:"secret"`
		}
		_ = json.Unmarshal(body, &probe)
		secretDigest := sha256.Sum256([]byte(probe.Secret))
		scope = op.ID + "\x00" + hex.EncodeToString(secretDigest[:])
	}
	digest := sha256.New()
	digest.Write([]byte(r.Method))
	digest.Write([]byte{0})
	digest.Write([]byte(r.URL.Path))
	digest.Write([]byte{0})
	digest.Write([]byte(r.URL.RawQuery))
	digest.Write([]byte{0})
	digest.Write([]byte(r.Header.Get(subjectTokenHeader)))
	digest.Write([]byte{0})
	digest.Write(body)
	requestHash := base64.RawURLEncoding.EncodeToString(digest.Sum(nil))

	stored, claimed, err := h.svc.Idempotency.Begin(r.Context(), scope, key, requestHash)
	switch {
	case err != nil && errors.Is(err, ErrConflict):
		h.writeError(w, r, http.StatusConflict, "idempotency_conflict", "the idempotency key was already used with a different request")
		return
	case err != nil:
		h.writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
		return
	case stored != nil:
		w.Header().Set("Idempotency-Replayed", "true")
		if stored.ContentType != "" {
			w.Header().Set("Content-Type", stored.ContentType)
		}
		w.WriteHeader(stored.Status)
		_, _ = w.Write(stored.Body)
		return
	case !claimed:
		h.writeError(w, r, http.StatusConflict, "idempotency_conflict", "the idempotency key is already executing")
		return
	}

	recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
	fn(recorder, r, rc)
	if recorder.status >= http.StatusInternalServerError {
		// Server-side failures are retryable; do not pin them.
		_ = h.svc.Idempotency.Abandon(r.Context(), scope, key)
		return
	}
	_ = h.svc.Idempotency.Complete(r.Context(), scope, key, StoredResponse{
		Status:      recorder.status,
		ContentType: recorder.Header().Get("Content-Type"),
		Body:        recorder.buffer.Bytes(),
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	buffer      bytes.Buffer
}

func (r *responseRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	r.buffer.Write(p)
	return r.ResponseWriter.Write(p)
}

// memoryIdempotencyStore is the in-process default. Entries expire after the
// TTL; expired entries are purged opportunistically.
type memoryIdempotencyStore struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	entries map[string]*idempotencyEntry
}

type idempotencyEntry struct {
	requestHash string
	inFlight    bool
	response    StoredResponse
	expiresAt   time.Time
}

func newMemoryIdempotencyStore(now func() time.Time, ttl time.Duration) *memoryIdempotencyStore {
	return &memoryIdempotencyStore{now: now, ttl: ttl, entries: map[string]*idempotencyEntry{}}
}

func (s *memoryIdempotencyStore) Begin(_ context.Context, scope, key, requestHash string) (*StoredResponse, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, entry := range s.entries {
		if now.After(entry.expiresAt) {
			delete(s.entries, k)
		}
	}
	mapKey := scope + "\x00" + key
	if entry, ok := s.entries[mapKey]; ok {
		if entry.requestHash != requestHash || entry.inFlight {
			return nil, false, ErrConflict
		}
		response := entry.response
		return &response, false, nil
	}
	s.entries[mapKey] = &idempotencyEntry{requestHash: requestHash, inFlight: true, expiresAt: now.Add(s.ttl)}
	return nil, true, nil
}

func (s *memoryIdempotencyStore) Complete(_ context.Context, scope, key string, response StoredResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.entries[scope+"\x00"+key]; ok {
		entry.inFlight = false
		entry.response = response
	}
	return nil
}

func (s *memoryIdempotencyStore) Abandon(_ context.Context, scope, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, scope+"\x00"+key)
	return nil
}

// --- service operations ----------------------------------------------------

type enrollWire struct {
	Secret string `json:"secret"`
	EnrollmentRequest
}

func (h *Handler) handleEnroll(w http.ResponseWriter, r *http.Request, _ *requestContext) {
	if h.svc.Enroller == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "enrollment is unavailable")
		return
	}
	var body enrollWire
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.Secret == "" || body.Kind == "" || body.InstanceID == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "secret, kind, and instance_id are required")
		return
	}
	// r.TLS carries the enrolling connection's client certificate, when any;
	// the enrollment service binds it to the application.
	credential, err := h.svc.Enroller.Enroll(r.Context(), body.Secret, body.EnrollmentRequest, r.TLS)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, credential)
}

func (h *Handler) handleRenewCredential(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	if h.svc.Renewer == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "credential renewal is unavailable")
		return
	}
	// The middleware already authenticated the bearer together with the
	// connection's TLS state; renewal reuses that verified identity instead
	// of re-authenticating with a possibly different connection view.
	credential, err := h.svc.Renewer.Renew(r.Context(), rc.app.ApplicationID)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, credential)
}

type healthWire struct {
	Status      string          `json:"status"`
	Version     string          `json:"version"`
	API         APIRange        `json:"api"`
	Application applicationWire `json:"application"`
}

type applicationWire struct {
	ApplicationID string   `json:"application_id"`
	Kind          string   `json:"kind"`
	InstanceID    string   `json:"instance_id"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	// The probe may report the companion's own health; without a report the
	// heartbeat is contact-only and recorded as unknown. A status outside
	// the vocabulary is refused before anything is recorded.
	status := HealthUnknown
	if raw := r.URL.Query().Get("status"); raw != "" {
		switch HealthStatus(raw) {
		case HealthHealthy, HealthDegraded, HealthUnhealthy:
			status = HealthStatus(raw)
		default:
			h.writeError(w, r, http.StatusBadRequest, "invalid_request", "status must be healthy, degraded, or unhealthy")
			return
		}
	}
	if h.svc.Heartbeats != nil {
		// Best effort: liveness bookkeeping must not fail the health probe.
		_ = h.svc.Heartbeats.Heartbeat(r.Context(), rc.app.ApplicationID, status, h.cfg.Now())
	}
	h.writeJSON(w, http.StatusOK, healthWire{
		Status:  "ok",
		Version: h.cfg.Version,
		API:     h.cfg.APIRange,
		Application: applicationWire{
			ApplicationID: rc.app.ApplicationID,
			Kind:          rc.app.Kind,
			InstanceID:    rc.app.InstanceID,
			Capabilities:  rc.app.Capabilities,
		},
	})
}
