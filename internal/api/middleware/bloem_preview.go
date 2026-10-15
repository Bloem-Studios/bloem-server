package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

// CodePreviewOnly is the refusal code a preview principal receives, in the
// v1 body and the X-Bloem-Denial header (v2 maps it to permission_denied).
const (
	CodePreviewOnly   = "preview_only"
	ReasonPreviewOnly = "preview_only"
)

// PreviewPrincipals resolves whether a caller is a preview principal.
// *auth.SessionRepository implements it.
type PreviewPrincipals interface {
	ActiveSessionAuthority(ctx context.Context, sessionID string) (role string, previewOnly, active bool, err error)
	AccountPreviewOnly(ctx context.Context, accountID int) (bool, error)
}

// PreviewRouteGuard reports whether a preview principal may use the route a
// request resolves to. Nil denies everything.
type PreviewRouteGuard func(r *http.Request) bool

// SetPreviewPrincipals installs the preview lookup. Without it no caller is
// treated as a preview principal, so production wiring must install it.
func (am *AuthMiddleware) SetPreviewPrincipals(p PreviewPrincipals) { am.previewPrincipals = p }

// SetPreviewRouteGuard installs the v1 and native preview route boundary.
func (am *AuthMiddleware) SetPreviewRouteGuard(guard PreviewRouteGuard) { am.previewRoutes = guard }

type previewPrincipalKey struct{}

// IsPreviewPrincipal reports whether RequireAuth authenticated the request as
// a preview principal.
func IsPreviewPrincipal(ctx context.Context) bool {
	marked, _ := ctx.Value(previewPrincipalKey{}).(bool)
	return marked
}

func withPreviewPrincipal(ctx context.Context) context.Context {
	return previewcontext.With(context.WithValue(ctx, previewPrincipalKey{}, true))
}

// WritePreviewOnly refuses a request because the caller is a preview principal.
func WritePreviewOnly(w http.ResponseWriter) {
	if recorder, ok := w.(DenialReasonRecorder); ok {
		recorder.RecordDenialReason(ReasonPreviewOnly)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Bloem-Denial", CodePreviewOnly)
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"forbidden","code":"preview_only","message":"This is not available in the preview."}`))
}

// sessionAuthority is the per-request session check, with the preview flag
// when a preview lookup is installed.
func (am *AuthMiddleware) sessionAuthority(ctx context.Context, sessionID string) (authority auth.SessionAuthority, previewOnly, active bool, err error) {
	if source, ok := am.previewPrincipals.(interface {
		ActivePreviewSession(context.Context, string) (auth.SessionAuthority, bool, bool, error)
	}); ok {
		return source.ActivePreviewSession(ctx, sessionID)
	}
	authority, active, err = am.sessionValidator.ActiveSession(ctx, sessionID)
	if err != nil || !active || am.previewPrincipals == nil {
		return authority, false, active, err
	}
	_, previewOnly, active, err = am.previewPrincipals.ActiveSessionAuthority(ctx, sessionID)
	return authority, previewOnly, active, err
}

// accountPreview reports whether an account is a preview principal, for the
// credentials that carry no session: API keys and audience tickets.
func (am *AuthMiddleware) accountPreview(ctx context.Context, accountID int) (bool, error) {
	if am.previewPrincipals == nil {
		return false, nil
	}
	return am.previewPrincipals.AccountPreviewOnly(ctx, accountID)
}

// CodePreviewSessionInvalid refuses a guest token that is not valid here:
// another host, the preview is off, or the session belongs to another account.
const CodePreviewSessionInvalid = "preview_session_invalid"

// SetPreviewGuests installs what guest admission needs. Without it a guest
// token is refused.
func (am *AuthMiddleware) SetPreviewGuests(settings auth.PreviewGuestSettingsSource, limiter *PreviewGuestLimiter) {
	am.previewGuests, am.previewGuestLimit = settings, limiter
}

type previewGuestKey struct{}

// IsPreviewGuest reports whether RequireAuth admitted a guest session.
func IsPreviewGuest(ctx context.Context) bool {
	marked, _ := ctx.Value(previewGuestKey{}).(bool)
	return marked
}

func withPreviewGuest(ctx context.Context) context.Context {
	return context.WithValue(ctx, previewGuestKey{}, true)
}

// admitPreviewGuest checks a guest token against the current preview: on,
// on its host (r.Host, never X-Forwarded-Host), for the shared account, and
// within the guest traffic cap. It writes the refusal itself.
func (am *AuthMiddleware) admitPreviewGuest(w http.ResponseWriter, r *http.Request, claims *auth.Claims) bool {
	if am.previewGuests == nil {
		writeUnauthorized(w, "Preview sessions are not available", CodePreviewSessionInvalid)
		return false
	}
	settings, err := am.previewGuests.PreviewGuestSettings(r.Context())
	if err != nil {
		writeCredentialCheckUnavailable(w, r, err)
		return false
	}
	if !settings.Enabled || settings.AccountID != claims.UserID || auth.HostOnly(r.Host) != settings.Host {
		writeUnauthorized(w, "This preview session is not valid here", CodePreviewSessionInvalid)
		return false
	}
	if am.previewGuestLimit != nil && !am.previewGuestLimit.Allow(claims.SessionID) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate_limited","code":"preview_busy","message":"The preview is busy; try again in a moment."}`))
		return false
	}
	return true
}

// PreviewGuestLimiter caps guest traffic: each guest, and all guests
// together, so guests take a bounded share of the server's budget.
type PreviewGuestLimiter struct {
	all      *rate.Limiter
	mu       sync.Mutex
	sessions map[string]*guestBucket
	pruneAt  time.Time
	now      func() time.Time
}

type guestBucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

const (
	previewGuestsPerSecond = 60
	previewGuestsBurst     = 120
	previewGuestPerSecond  = 10
	previewGuestBurst      = 30
)

func NewPreviewGuestLimiter() *PreviewGuestLimiter {
	return &PreviewGuestLimiter{
		all:      rate.NewLimiter(previewGuestsPerSecond, previewGuestsBurst),
		sessions: make(map[string]*guestBucket),
		now:      time.Now,
	}
}

// Allow spends one request for a guest session, or reports that it must wait.
func (l *PreviewGuestLimiter) Allow(sessionID string) bool {
	now := l.now()
	l.mu.Lock()
	if now.After(l.pruneAt) {
		for id, b := range l.sessions {
			if now.Sub(b.seen) > 30*time.Minute {
				delete(l.sessions, id)
			}
		}
		l.pruneAt = now.Add(time.Minute)
	}
	b := l.sessions[sessionID]
	if b == nil {
		b = &guestBucket{limiter: rate.NewLimiter(previewGuestPerSecond, previewGuestBurst)}
		l.sessions[sessionID] = b
	}
	b.seen = now
	l.mu.Unlock()
	if !b.limiter.AllowN(now, 1) {
		return false
	}
	return l.all.AllowN(now, 1)
}

// AllowGlobal bounds public guest requests as well as authenticated browsing.
func (l *PreviewGuestLimiter) AllowGlobal() bool { return l.all.Allow() }
