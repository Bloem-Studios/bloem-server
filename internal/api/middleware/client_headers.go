package middleware

import (
	"net/http"
	"net/textproto"
	"strings"
)

// clientHeaderAliases maps each canonical device/client identity header to the
// vendor-branded spelling a Bloem client sends instead.
//
// The server was forked from Silo and every handler reads the X-Silo-* name.
// The native Bloem clients have always sent X-Bloem-* for the same fields, so
// the two halves of the device identity never met: X-Silo-Client-Family was
// read straight from the header with no fallback, which is why profile_client
// ("like client") settings roaming never resolved for any Bloem client. Device
// id only appeared to work because a direct-profile session backfills it from
// the session's device binding in RequireAuth — the client's own header was
// simply discarded.
//
// Both spellings are accepted indefinitely. An upstream-compatible client that
// sends only X-Silo-* is untouched by this table: for such a request every
// canonical header is already present (or already absent), so nothing here
// writes, deletes, or reorders a single byte of it.
var clientHeaderAliases = map[string]string{
	"X-Silo-Device-Id":       "X-Bloem-Device-Id",
	"X-Silo-Device-Name":     "X-Bloem-Device-Name",
	"X-Silo-Device-Platform": "X-Bloem-Device-Platform",
	"X-Silo-Client-Family":   "X-Bloem-Client-Family",
	"X-Silo-Client":          "X-Bloem-Client",
	"X-Silo-Client-Version":  "X-Bloem-Client-Version",
	"X-Silo-Client-Build":    "X-Bloem-Client-Build",
	"X-Silo-Client-Channel":  "X-Bloem-Client-Channel",
}

// NormalizeClientHeaders folds the X-Bloem-* device/client identity headers
// onto the canonical X-Silo-* names every handler already reads, so exactly one
// spelling exists downstream and no handler has to learn about two.
//
// It is mounted at the root of the native chain, ahead of authentication. That
// ordering is load-bearing rather than incidental: RequireAuth treats
// X-Silo-Device-Id as a *declared* device and refuses a value that conflicts
// with the session's binding before overwriting it from the claims. Folding the
// alias in first means a device id declared under either spelling faces that
// same guard. Folding it in afterwards would let a session declare
// X-Bloem-Device-Id and act as a device it never authenticated — the guard
// would have already run against a header that was empty at the time.
//
// Precedence when both spellings are present: the canonical X-Silo-* value
// wins. A request that already carries the canonical header must keep behaving
// exactly as it does today, and the plausible ways both names arrive at once —
// an intermediary mirroring or injecting identity, a client mid-migration
// sending old and new — are all cases where the value the server has always
// honored should stay authoritative. The alias only ever fills a gap; it never
// overrides.
//
// An empty (or all-whitespace) value is not a present value on either side: a
// canonical header sent empty is a gap the alias may fill, and an empty alias
// fills nothing.
//
// The alias headers are removed once folded, so the canonical name is the only
// device identity in the request from here on. That is deliberate: leaving a
// stale X-Bloem-Device-Id in place would preserve the caller's unvalidated,
// pre-guard claim next to the authenticated one, and a later reader reaching
// for it would silently bypass the RequireAuth check above.
func NormalizeClientHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for canonical, alias := range clientHeaderAliases {
			aliasKey := textproto.CanonicalMIMEHeaderKey(alias)
			values, sent := r.Header[aliasKey]
			if !sent {
				continue
			}
			delete(r.Header, aliasKey)
			if strings.TrimSpace(r.Header.Get(canonical)) != "" {
				continue
			}
			for _, value := range values {
				if strings.TrimSpace(value) == "" {
					continue
				}
				r.Header.Set(canonical, value)
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}
