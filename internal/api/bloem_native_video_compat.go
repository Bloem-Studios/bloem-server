package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Jellyfin clients play native-storage files through Bloem's native video
// delivery. The Jellyfin listener keeps Silo's flow (sessions, stream limits,
// routing, progress and teardown); only the step that turns a path into bytes
// goes to NativeVideoCompat, through playback's context-scoped media host.
//
// Authority: the compatibility session is the credential. Every open and
// every authority check rechecks it (the stored row and its login session),
// the account, the profile and its PIN and credential revisions, the
// organization membership, the viewer's playback and library scope, the
// source, and the playback session it serves. The compatibility sign-in
// verified the PIN, so it is not asked again. Sessions synthesized for an
// sa_ admin key have no login session and are refused.

// nativeVideoCompatTokenType marks an authority delegated from a
// Jellyfin-compatible session.
const nativeVideoCompatTokenType = "jellycompat"

// NativeVideoCompatSession is a compatibility session as it stands now.
type NativeVideoCompatSession struct {
	UserID         int
	ProfileID      string
	LoginSessionID string
	ExpiresAt      time.Time
}

// NativeVideoCompatSessions reads Jellyfin-compatible sessions. Recheck reads
// a session by its token past any replica cache; Live reports whether the
// session with this token digest is still stored for the account and profile.
type NativeVideoCompatSessions interface {
	Recheck(ctx context.Context, credential string) (NativeVideoCompatSession, error)
	Live(ctx context.Context, userID int, profileID, digest string) error
}

// NativeVideoCompat is the Jellyfin listener's native media host. The API
// router installs native delivery into it; the listener supplies its
// sessions. Until both are present it serves nothing.
type NativeVideoCompat struct {
	sessions atomic.Pointer[nativeVideoCompatSessionsBox]
	delivery atomic.Pointer[nativeVideoCompatDelivery]
}

type nativeVideoCompatSessionsBox struct{ NativeVideoCompatSessions }

type nativeVideoCompatDelivery struct {
	service    *handlers.NativeVideoService
	getSession func(string) (*playback.Session, error)
	files      handlers.FilePathResolver
}

func NewNativeVideoCompat() *NativeVideoCompat { return &NativeVideoCompat{} }

// SetSessions supplies the Jellyfin listener's session store.
func (c *NativeVideoCompat) SetSessions(sessions NativeVideoCompatSessions) {
	if c != nil && sessions != nil {
		c.sessions.Store(&nativeVideoCompatSessionsBox{sessions})
	}
}

func (c *NativeVideoCompat) currentSessions() NativeVideoCompatSessions {
	if c == nil {
		return nil
	}
	if box := c.sessions.Load(); box != nil {
		return box.NativeVideoCompatSessions
	}
	return nil
}

func (c *NativeVideoCompat) install(service *handlers.NativeVideoService, getSession func(string) (*playback.Session, error), files handlers.FilePathResolver) {
	if c != nil && service != nil && getSession != nil && files != nil {
		c.delivery.Store(&nativeVideoCompatDelivery{service: service, getSession: getSession, files: files})
	}
}

// Ready reports whether native delivery and the session store are both in
// place.
func (c *NativeVideoCompat) Ready() bool {
	return c != nil && c.delivery.Load() != nil && c.currentSessions() != nil
}

// Handles reports whether path is a native-storage catalog reference.
func (c *NativeVideoCompat) Handles(path string) bool { return storagesource.IsNativeLocation(path) }

// ServeDirect serves a Jellyfin direct-play request (GET, HEAD and ranges)
// from the request's playback session, through native direct delivery.
func (c *NativeVideoCompat) ServeDirect(w http.ResponseWriter, r *http.Request, path string) error {
	delivery, session, file, err := c.bind(r.Context(), path)
	if err == nil {
		err = delivery.service.Serve(w, r, session, file)
		if err == nil {
			return nil
		}
	}
	status := playback.ContextMediaStatus(nativeVideoCompatError(err))
	if status == 499 {
		return err
	}
	http.Error(w, http.StatusText(status), status)
	return err
}

// Input returns the request's pinned source for remux, HLS and seek probes.
func (c *NativeVideoCompat) Input(ctx context.Context, path string) (playback.TranscodeInputBinding, error) {
	delivery, session, file, err := c.bind(ctx, path)
	if err != nil {
		return playback.TranscodeInputBinding{}, nativeVideoCompatError(err)
	}
	source, format, err := delivery.service.InputSource(ctx, session, file)
	if err != nil {
		return playback.TranscodeInputBinding{}, nativeVideoCompatError(err)
	}
	return playback.TranscodeInputBinding{InputPath: file.FilePath, InputFormat: format, SessionID: session.ID, InputSource: source}, nil
}

// bind loads the request's playback session and file and binds the native
// source to the session under the compatibility session's authority.
func (c *NativeVideoCompat) bind(ctx context.Context, path string) (*nativeVideoCompatDelivery, *playback.Session, *models.MediaFile, error) {
	if c == nil || c.currentSessions() == nil {
		return nil, nil, nil, errNativeVideoCompatUnavailable
	}
	delivery := c.delivery.Load()
	if delivery == nil {
		return nil, nil, nil, errNativeVideoCompatUnavailable
	}
	stream, ok := playback.ContextStreamFrom(ctx)
	if !ok || stream.PlaybackSessionID == "" || stream.MediaFileID <= 0 {
		return nil, nil, nil, middleware.ErrCurrentCredentialForbidden
	}
	current, err := delivery.getSession(stream.PlaybackSessionID)
	if err != nil {
		return nil, nil, nil, err
	}
	if current == nil || current.ID != stream.PlaybackSessionID || current.UserID != stream.UserID || current.ProfileID != stream.ProfileID || current.MediaFileID != stream.MediaFileID {
		return nil, nil, nil, middleware.ErrCurrentCredentialForbidden
	}
	file, err := delivery.files.GetByID(ctx, stream.MediaFileID)
	if err != nil {
		return nil, nil, nil, err
	}
	if file == nil || file.ID != stream.MediaFileID || file.FilePath != path || !handlers.NativeVideoSupported(file) || handlers.NativeAudioSupported(file) {
		return nil, nil, nil, catalog.ErrItemNotFound
	}
	session := *current
	if err := delivery.service.Bind(ctx, &session, file); err != nil {
		return nil, nil, nil, err
	}
	return delivery, &session, file, nil
}

var errNativeVideoCompatUnavailable = errors.New("native video for Jellyfin clients is unavailable")

type nativeVideoCompatStatusError struct {
	status int
	err    error
}

func (e nativeVideoCompatStatusError) Error() string   { return e.err.Error() }
func (e nativeVideoCompatStatusError) Unwrap() error   { return e.err }
func (e nativeVideoCompatStatusError) HTTPStatus() int { return e.status }

// nativeVideoCompatError gives a refusal its response status. Authority
// refusals answer 403, never 401: a Jellyfin client signs out on 401.
func nativeVideoCompatError(err error) error {
	if err == nil {
		return nil
	}
	status := http.StatusServiceUnavailable
	var apiErr *handlers.APIError
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.As(err, &apiErr) && apiErr.Status >= 400:
		status = apiErr.Status
	case errors.Is(err, middleware.ErrCurrentCredentialInvalid), errors.Is(err, middleware.ErrCurrentCredentialForbidden):
		status = http.StatusForbidden
	case errors.Is(err, catalog.ErrItemNotFound), errors.Is(err, scanner.ErrFileNotFound), errors.Is(err, playback.ErrSessionNotFound), errors.Is(err, storagesource.ErrReferenceConflict):
		status = http.StatusNotFound
	}
	if status == http.StatusUnauthorized {
		status = http.StatusForbidden
	}
	return nativeVideoCompatStatusError{status: status, err: err}
}

// nativeVideoCompatSubject is the compatibility half of a native video
// subject. The credential is request memory only.
type nativeVideoCompatSubject struct {
	credential     string
	sessionID      string
	loginSessionID string
	expiresAt      time.Time
}

func nativeVideoCompatCapture(ctx context.Context, p nativeVideoPolicy, stream playback.ContextStream) (*nativeVideoSubject, error) {
	if p.compat == nil || p.viewer == nil || p.tenants == nil || p.resolveFile == nil || p.authorizeSource == nil || p.getSession == nil || p.denied == nil ||
		stream.Credential == "" || stream.UserID <= 0 || stream.PlaybackSessionID == "" || stream.MediaFileID <= 0 {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	subject := &nativeVideoSubject{
		policy: p, userID: stream.UserID, profileID: stream.ProfileID, fileID: stream.MediaFileID, skipPIN: true,
		request: nativeVideoRequest{method: http.MethodGet, path: nativeVideoCompatTokenType},
		compat:  &nativeVideoCompatSubject{credential: stream.Credential, sessionID: stream.PlaybackSessionID},
	}
	fresh, err := subject.refresh(ctx)
	if err != nil {
		return nil, err
	}
	tenant, ok := tenancyFromContext(fresh)
	if !ok {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	subject.tenant = &tenant
	return subject, nil
}

// verifyCompat rechecks the compatibility session and the playback session
// it serves, and returns the login session the viewer scope is resolved for.
func (s *nativeVideoSubject) verifyCompat(ctx context.Context) (string, error) {
	sessions := s.policy.compat.currentSessions()
	if sessions == nil {
		return "", errNativeVideoCompatUnavailable
	}
	current, err := sessions.Recheck(ctx, s.compat.credential)
	if err != nil {
		return "", err
	}
	if current.UserID != s.userID || current.ProfileID != s.profileID || current.LoginSessionID == "" || strings.HasPrefix(s.compat.credential, "sa_") {
		return "", middleware.ErrCurrentCredentialForbidden
	}
	if s.compat.loginSessionID != "" && current.LoginSessionID != s.compat.loginSessionID {
		return "", middleware.ErrCurrentCredentialForbidden
	}
	s.compat.loginSessionID, s.compat.expiresAt = current.LoginSessionID, current.ExpiresAt
	if s.policy.denied(ctx, s.compat.sessionID) {
		return "", middleware.ErrCurrentCredentialForbidden
	}
	session, err := s.policy.getSession(s.compat.sessionID)
	if err != nil {
		return "", err
	}
	if session == nil || session.ID != s.compat.sessionID || session.UserID != s.userID || session.ProfileID != s.profileID || session.MediaFileID != s.fileID {
		return "", middleware.ErrCurrentCredentialForbidden
	}
	return current.LoginSessionID, nil
}

func nativeVideoCompatDigest(credential string) string {
	sum := sha256.Sum256([]byte(credential))
	return hex.EncodeToString(sum[:])
}

// nativeVideoCompatDelegation fills an authority delegated from a
// compatibility session. It expires with the compatibility session, its login
// session, or the longest playback token lifetime, whichever is first.
func nativeVideoCompatDelegation(ctx context.Context, subject *nativeVideoSubject, p nativeVideoPolicy, d *handlers.NativeVideoAuthority) error {
	if subject.compat.loginSessionID == "" || p.loadLogin == nil {
		return middleware.ErrCurrentCredentialInvalid
	}
	d.TokenType, d.AuthMethod = nativeVideoCompatTokenType, nativeVideoCompatTokenType
	d.LoginSessionID = subject.compat.loginSessionID
	d.PlaybackSessionID = subject.compat.sessionID
	d.CompatSessionDigest = nativeVideoCompatDigest(subject.compat.credential)
	d.ExpiresAt = time.Now().UTC().Add(playback.MaxTokenTTL)
	if !subject.compat.expiresAt.IsZero() && subject.compat.expiresAt.Before(d.ExpiresAt) {
		d.ExpiresAt = subject.compat.expiresAt.UTC()
	}
	login, err := p.loadLogin(ctx, d.LoginSessionID)
	if err != nil {
		return err
	}
	if login == nil {
		return middleware.ErrCurrentCredentialInvalid
	}
	if login.ExpiresAt.Before(d.ExpiresAt) {
		d.ExpiresAt = login.ExpiresAt.UTC()
	}
	return nil
}

// verifyNativeVideoCompatActor is verifyNativeVideoActor's check of a
// compatibility delegation: its login session is live and its compatibility
// session is still stored.
func verifyNativeVideoCompatActor(ctx context.Context, d handlers.NativeVideoAuthority, p nativeVideoPolicy) error {
	if d.LoginSessionID == "" || d.PlaybackSessionID == "" || d.CompatSessionDigest == "" || p.loadLogin == nil || p.loginActive == nil {
		return middleware.ErrCurrentCredentialInvalid
	}
	sessions := p.compat.currentSessions()
	if sessions == nil {
		return middleware.ErrCurrentCredentialInvalid
	}
	login, err := p.loadLogin(ctx, d.LoginSessionID)
	if err != nil {
		return err
	}
	if login == nil || login.ID != d.LoginSessionID || login.UserID != d.UserID || login.RevokedAt != nil || !time.Now().Before(login.ExpiresAt) {
		return middleware.ErrCurrentCredentialInvalid
	}
	if login.ProfileID != nil && *login.ProfileID != d.ProfileID {
		return middleware.ErrCurrentCredentialInvalid
	}
	_, active, err := p.loginActive(ctx, d.LoginSessionID, d.UserID)
	if err != nil {
		return err
	}
	if !active {
		return middleware.ErrCurrentCredentialInvalid
	}
	if err := sessions.Live(ctx, d.UserID, d.ProfileID, d.CompatSessionDigest); err != nil {
		return fmt.Errorf("%w: %w", middleware.ErrCurrentCredentialInvalid, err)
	}
	return nil
}

func tenancyFromContext(ctx context.Context) (tenancy.Context, bool) {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || tenant.OrganizationID == uuid.Nil {
		return tenant, false
	}
	return tenant, true
}

// NewNativeVideoCompatSessions reads compatibility sessions: recheck reads one
// by token through the Jellyfin listener's session store, and the stored rows
// answer whether a delegated authority's session still exists.
func NewNativeVideoCompatSessions(pool *pgxpool.Pool, recheck func(context.Context, string) (NativeVideoCompatSession, error)) NativeVideoCompatStore {
	return NativeVideoCompatStore{pool: pool, recheck: recheck}
}

// NativeVideoCompatStore implements NativeVideoCompatSessions.
type NativeVideoCompatStore struct {
	pool    *pgxpool.Pool
	recheck func(context.Context, string) (NativeVideoCompatSession, error)
}

func (s NativeVideoCompatStore) Recheck(ctx context.Context, credential string) (NativeVideoCompatSession, error) {
	if s.recheck == nil {
		return NativeVideoCompatSession{}, errNativeVideoCompatUnavailable
	}
	return s.recheck(ctx, credential)
}

var errNativeVideoCompatSessionGone = errors.New("compatibility session ended")

func (s NativeVideoCompatStore) Live(ctx context.Context, userID int, profileID, digest string) error {
	if s.pool == nil {
		return errNativeVideoCompatUnavailable
	}
	var live bool
	// A user has few compat rows; the account index bounds the hash scan.
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jellycompat_sessions
		WHERE streamapp_user_id=$1 AND profile_id=$2 AND expires_at > now()
		  AND encode(sha256(convert_to(token, 'UTF8')), 'hex')=$3)`, userID, profileID, digest).Scan(&live)
	if err != nil {
		return err
	}
	if !live {
		return errNativeVideoCompatSessionGone
	}
	return nil
}
