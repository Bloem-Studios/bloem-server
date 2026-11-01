package clientip

import "net/http"

// RequestHostOf is the host[:port] req addressed, judged as Middleware judges
// it: the single X-Forwarded-Host a trusted proxy sent, else the Host header.
// Unlike Middleware it changes nothing on req, so a caller that only needs the
// host can run before Middleware without disturbing it. A nil resolver trusts
// no proxy. Bloem-owned (internal/orgdomains maps the host to an organization).
func (r *Resolver) RequestHostOf(req *http.Request) string {
	return r.requestHost(req)
}
