package artworkurl

import (
	"net/url"
	"strconv"
	"strings"
	"time"
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
