package artworkurl

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

// SignWindow signs key with a URL that stays the same for a whole window and
// is valid for at least window: expiry falls at the end of the window after the
// current one. It is for revisioned keys whose bytes never change, where a URL
// that outlives a day lets clients keep the bytes instead of downloading them
// again under a new URL.
func (s *Signer) SignWindow(key string, now time.Time, window time.Duration) (string, time.Time) {
	expires := now.UTC().Truncate(window).Add(2 * window)
	exp := expires.Unix()
	route := &url.URL{Path: s.route + strings.TrimPrefix(key, "/") + s.suffix()}
	return route.EscapedPath() + "?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + s.signature(key, exp), expires
}

// WindowResolver signs every key with SignWindow.
type WindowResolver struct {
	signer *Signer
	window time.Duration
}

func NewWindowResolver(signer *Signer, window time.Duration) Resolver {
	return WindowResolver{signer: signer, window: window}
}
func (r WindowResolver) ResolveURLs(ctx context.Context, keys []string) map[string]catalog.ResolvedImageURL {
	out := make(map[string]catalog.ResolvedImageURL, len(keys))
	now := time.Now()
	for _, key := range keys {
		if ctx.Err() != nil {
			break
		}
		url, exp := r.signer.SignWindow(key, now, r.window)
		out[key] = catalog.ResolvedImageURL{URL: url, ExpiresAt: &exp}
	}
	return out
}
