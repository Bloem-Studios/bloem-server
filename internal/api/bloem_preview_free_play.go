package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/preview"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// bloemFreePlayDelivery meters free play where media requests are authorized.
// A plan's playlist and segment URLs carry a stream token, which skips bearer
// authentication and the preview gate, so the shared preview account's
// tokens are judged here instead.
type bloemFreePlayDelivery struct {
	pool     *pgxpool.Pool
	settings *preview.Reader
	sessions interface{ StopSession(string) error }
}

func newBloemFreePlayDelivery(deps Dependencies) *bloemFreePlayDelivery {
	reader := bloemPreviewReader(deps)
	if reader == nil {
		return nil
	}
	d := &bloemFreePlayDelivery{pool: deps.DB, settings: reader}
	if deps.SessionMgr != nil {
		d.sessions = deps.SessionMgr
	}
	return d
}

// bloemStreamTokenAuth is the host's stream-token authorization with free play
// in front of it. Every other account's token passes straight through.
func bloemStreamTokenAuth(am *apimw.AuthMiddleware, secret string, freePlay *bloemFreePlayDelivery) func(http.Handler) http.Handler {
	authorize := am.StreamTokenAuth(secret)
	if freePlay == nil || secret == "" {
		return authorize
	}
	return func(next http.Handler) http.Handler {
		authorized := authorize(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.URL.Query().Get(streamtoken.QueryParam)
			if raw == "" {
				authorized.ServeHTTP(w, r)
				return
			}
			claims, err := streamtoken.Verify(raw, secret)
			if err != nil || claims == nil || !freePlay.previewAccount(r.Context(), claims.UserID) {
				authorized.ServeHTTP(w, r)
				return
			}
			ctx, ok := freePlay.admit(w, r, claims.SessionID)
			if !ok {
				return
			}
			authorized.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// previewAccount reports whether account is the shared preview account. A
// failed settings read answers no: the token then gets the account's own
// policy, which forbids playback.
func (d *bloemFreePlayDelivery) previewAccount(ctx context.Context, account int) bool {
	if account <= 0 {
		return false
	}
	settings, err := d.settings.Settings(ctx)
	return err == nil && settings.AccountID == account
}

type freePlayDeliveryKind int

const (
	freePlayNotDelivery freePlayDeliveryKind = iota
	freePlayPlaylist
	freePlaySegment
	freePlaySubtitle
)

// freePlayDeliveryPath classifies a media path for free play: an HLS playlist
// or segment of a transcode or remux session, or a subtitle of a session.
// Everything else, the progressive original included, is not free play.
func freePlayDeliveryPath(path string) (string, freePlayDeliveryKind) {
	if i := strings.Index(path, "/playback/transcode/"); i >= 0 {
		session, tail, ok := strings.Cut(path[i+len("/playback/transcode/"):], "/")
		switch {
		case !ok || session == "":
		case tail == "master.m3u8" || tail == "media.m3u8":
			return session, freePlayPlaylist
		case strings.HasPrefix(tail, "segment/") && len(tail) > len("segment/") && !strings.Contains(tail[len("segment/"):], "/"):
			return session, freePlaySegment
		}
		return "", freePlayNotDelivery
	}
	if i := strings.Index(path, "/stream/"); i >= 0 {
		session, tail, ok := strings.Cut(path[i+len("/stream/"):], "/")
		if ok && session != "" && strings.HasPrefix(tail, "subtitles/") && len(tail) > len("subtitles/") {
			return session, freePlaySubtitle
		}
	}
	return "", freePlayNotDelivery
}

// admit lets a preview-account delivery through only for a live grant that
// started its playback session, charging the meter for a segment. A spent
// grant ends the playback session.
func (d *bloemFreePlayDelivery) admit(w http.ResponseWriter, r *http.Request, tokenSession string) (context.Context, bool) {
	session, kind := freePlayDeliveryPath(r.URL.Path)
	if kind == freePlayNotDelivery || session != tokenSession {
		apimw.WritePreviewOnly(w)
		return nil, false
	}
	grant, ok := d.meter(w, r, session, kind)
	if !ok {
		return nil, false
	}
	return previewcontext.WithFreePlay(previewcontext.With(r.Context()), grant), true
}

// Deliver is admit for a bearer media request, which guest admission already
// marked: the playback session must belong to the guest's own grant.
func (d *bloemFreePlayDelivery) Deliver(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	own, _ := previewcontext.FreePlayGrant(r.Context())
	session, kind := freePlayDeliveryPath(r.URL.Path)
	if kind == freePlayNotDelivery {
		apimw.WritePreviewOnly(w)
		return nil, false
	}
	// Only a session the guest's own grant started is judged at all, so a
	// request naming another session neither plays it nor stops it.
	bound, ok, err := preview.PlaybackGrant(r.Context(), d.pool, session)
	if err != nil {
		writeFreePlayUnavailable(w)
		return nil, false
	}
	if !ok || bound != own {
		apimw.WritePreviewOnly(w)
		return nil, false
	}
	if _, ok := d.meter(w, r, session, kind); !ok {
		return nil, false
	}
	return r, true
}

// Busy reports whether the preview's free-play capacity is streaming for
// other grants.
func (d *bloemFreePlayDelivery) Busy(ctx context.Context, grant string) (bool, error) {
	settings, err := d.settings.Settings(ctx)
	if err != nil {
		return false, err
	}
	return preview.FreePlayBusy(ctx, d.pool, grant, settings.FreePlayMaxActive)
}

func (d *bloemFreePlayDelivery) BindPlayback(ctx context.Context, playback, grant string) error {
	return preview.BindPlayback(ctx, d.pool, playback, grant)
}

func (d *bloemFreePlayDelivery) StopPlayback(playback string) {
	if d.sessions != nil {
		_ = d.sessions.StopSession(playback)
	}
}

// meter charges a segment or checks a playlist or subtitle, writing the
// refusal and stopping the playback session when the grant is spent.
func (d *bloemFreePlayDelivery) meter(w http.ResponseWriter, r *http.Request, session string, kind freePlayDeliveryKind) (string, bool) {
	var grant string
	var live bool
	var err error
	if kind == freePlaySegment {
		grant, live, err = preview.ChargeDelivery(r.Context(), d.pool, session)
	} else {
		grant, live, err = preview.LivePlayback(r.Context(), d.pool, session)
	}
	if err != nil {
		writeFreePlayUnavailable(w)
		return "", false
	}
	if !live {
		d.StopPlayback(session)
		apimw.WriteFreePlayEnded(w)
		return "", false
	}
	return grant, true
}

// bloemPreviewTokenViewer narrows the scope a stream-token request resolves,
// as the bearer path's viewer does, so a metered free-play delivery may play
// within the preview's libraries. Unmarked requests are untouched.
type bloemPreviewTokenViewer struct {
	inner apimw.ViewerResolver
	pool  *pgxpool.Pool
}

func (v bloemPreviewTokenViewer) Resolve(ctx context.Context, input access.ResolveInput) (access.Scope, error) {
	scope, err := v.inner.Resolve(ctx, input)
	if err != nil {
		return access.Scope{}, err
	}
	return narrowBloemPreviewScope(ctx, v.pool, scope)
}

func writeFreePlayUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"unavailable","message":"Free play is temporarily unavailable."}`))
}
