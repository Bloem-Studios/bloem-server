// Package compatgateway implements the fixed-path edge gateway that fronts
// the removable compatibility applications on Vondel's canonical public
// address. Route ownership is compile-time, reviewed configuration: Jellyfin
// owns its explicit protocol route families plus /web, Audiobookshelf owns
// only /audiobookshelf/**, and nothing can add a route at runtime. Native
// surfaces — the Vondel application at "/", /api/v1/**, /api/v2/**, and
// /metrics — are never claimed.
package compatgateway

import "strings"

// AppKind names a compatibility application.
type AppKind string

const (
	// KindJellyfin is the removable vondel-jellyfin application.
	KindJellyfin AppKind = "jellyfin"
	// KindAudiobookshelf is the removable vondel-audiobookshelf application.
	KindAudiobookshelf AppKind = "audiobookshelf"
)

// Route is one fixed, reviewed path family owned by a compatibility
// application. Prefix is a single leading path segment; ownership covers the
// segment itself and everything beneath it.
type Route struct {
	App AppKind
	// Prefix is the owned path family, e.g. "/System" or "/audiobookshelf".
	Prefix string
	// StripPrefix removes the public prefix before the request is forwarded,
	// for applications whose protocol adapter expects protocol-native paths.
	StripPrefix bool
	// CaseInsensitive matches the prefix segment ignoring case. The Jellyfin
	// protocol treats paths case-insensitively; the Audiobookshelf public
	// prefix is exact.
	CaseInsensitive bool
}

const (
	// audiobookshelfPrefix is the only path family Audiobookshelf owns.
	audiobookshelfPrefix = "/audiobookshelf"
	// jellyfinWebPrefix is the Jellyfin Web application mount.
	jellyfinWebPrefix = "/web"
)

// jellyfinPrefixes is the reviewed fixed Jellyfin protocol route set. It is
// the first-segment closure of the embedded Jellyfin-compatibility listener:
// system, users, items, sessions, and Live TV families, their satellite
// families, and the Jellyfin Web application at /web. The native application
// root "/" is deliberately absent — Vondel owns it.
var jellyfinPrefixes = []string{
	"/Artists",
	"/Branding",
	"/ClientLog",
	"/DisplayPreferences",
	"/Episode",
	"/Genres",
	"/Items",
	"/Library",
	"/LiveStreams",
	"/LiveTv",
	"/MediaSegments",
	"/Movies",
	"/Persons",
	"/Playback",
	"/QuickConnect",
	"/Search",
	"/Sessions",
	"/Shows",
	"/socket",
	"/Studios",
	"/System",
	"/UserFavoriteItems",
	"/UserImage",
	"/UserItems",
	"/UserPlayedItems",
	"/Users",
	"/UserViews",
	"/Videos",
	jellyfinWebPrefix,
}

// routeTable is the static ownership table. It is never exposed directly:
// callers receive copies, so the table cannot be extended or edited at
// runtime.
var routeTable = buildRouteTable()

func buildRouteTable() []Route {
	table := make([]Route, 0, len(jellyfinPrefixes)+1)
	for _, prefix := range jellyfinPrefixes {
		table = append(table, Route{App: KindJellyfin, Prefix: prefix, CaseInsensitive: true})
	}
	table = append(table, Route{App: KindAudiobookshelf, Prefix: audiobookshelfPrefix, StripPrefix: true})
	return table
}

// RouteTable returns a copy of the fixed route table. Mutating the returned
// slice has no effect on gateway matching.
func RouteTable() []Route {
	table := make([]Route, len(routeTable))
	copy(table, routeTable)
	return table
}

// MatchPath resolves the application owning a request path. Ownership is
// decided per whole path segment: "/SystemX" is not owned by "/System".
func MatchPath(path string) (Route, bool) {
	segment := firstSegment(path)
	if segment == "" {
		return Route{}, false
	}
	for _, route := range routeTable {
		owned := strings.TrimPrefix(route.Prefix, "/")
		if route.CaseInsensitive {
			if strings.EqualFold(segment, owned) {
				return route, true
			}
			continue
		}
		if segment == owned {
			return route, true
		}
	}
	return Route{}, false
}

// firstSegment returns the first path segment without surrounding slashes.
func firstSegment(path string) string {
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return ""
	}
	if idx := strings.IndexByte(trimmed, '/'); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}
