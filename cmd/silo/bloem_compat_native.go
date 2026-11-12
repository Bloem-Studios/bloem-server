package main

// Jellyfin clients play native-storage files through Bloem's native video
// delivery (internal/api NativeVideoCompat). This file scopes that host to the
// Jellyfin listener's stream routes and supplies its session store.

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/jellycompat"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// compatNativeMediaHost is a context media host that may not be ready yet:
// the router installs native delivery after the listener is wired.
type compatNativeMediaHost interface {
	playback.ContextMediaHost
	Ready() bool
}

// bloemCompatNativeMediaListener gives the Jellyfin listener native media:
// the host receives the listener's sessions and is scoped to stream routes.
func bloemCompatNativeMediaListener(next http.Handler, host *api.NativeVideoCompat, sessions *jellycompat.SessionStore, pool *pgxpool.Pool) http.Handler {
	if host == nil || sessions == nil || pool == nil {
		return next
	}
	host.SetSessions(api.NewNativeVideoCompatSessions(pool, func(ctx context.Context, credential string) (api.NativeVideoCompatSession, error) {
		session, err := sessions.Recheck(ctx, credential)
		if err != nil {
			return api.NativeVideoCompatSession{}, err
		}
		return api.NativeVideoCompatSession{UserID: session.StreamAppUserID, ProfileID: session.ProfileID, LoginSessionID: session.LoginSessionID, ExpiresAt: session.ExpiresAt}, nil
	}))
	return bloemCompatNativeMedia(next, host)
}

// bloemCompatNativeMedia installs host on the Jellyfin stream routes: video
// streams, HLS playlists and segments, subtitles and attachments. The native
// file guard opens only inside such a request, and the host authorizes every
// byte it serves.
func bloemCompatNativeMedia(next http.Handler, host compatNativeMediaHost) http.Handler {
	if host == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host.Ready() && compatNativeMediaRoute(r.URL.Path) {
			r = r.WithContext(playback.WithContextMediaHost(r.Context(), host))
		}
		next.ServeHTTP(w, r)
	})
}

func compatNativeMediaRoute(path string) bool {
	lower := strings.ToLower(path)
	at := strings.Index(lower, "/videos/")
	if at < 0 {
		return false
	}
	rest := lower[at+len("/videos/"):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return false
	}
	rest = rest[slash:]
	return strings.HasPrefix(rest, "/stream") || strings.HasSuffix(rest, ".m3u8") ||
		strings.HasPrefix(rest, "/hls1/") || strings.HasPrefix(rest, "/hls/") ||
		strings.Contains(rest, "/subtitles/") || strings.Contains(rest, "/attachments/")
}
