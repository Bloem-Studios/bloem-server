package livetv

import (
	"net/http"
	"time"
)

// xtreamRefuseRedirect never follows a provider redirect. Provider
// credentials appear in both queries and paths, so they must not be forwarded
// to a redirect target, including another path on the same host.
func xtreamRefuseRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// newXtreamGuideHTTPClient builds the client for the XMLTV download. XMLTV
// documents can be tens of megabytes from slow providers, so, unlike
// NewMediaHTTPClient, there is no whole-request Client.Timeout covering the
// body. The SSRF-guarded dialer, dial/TLS timeouts, the response-header
// timeout and redirect refusal are unchanged; the body is bounded by the
// guide sync context deadline and the parser's xtreamXMLLimit.
func newXtreamGuideHTTPClient(headerTimeout time.Duration) *http.Client {
	transport := newMediaTransport()
	transport.ResponseHeaderTimeout = headerTimeout
	return &http.Client{Transport: transport, CheckRedirect: xtreamRefuseRedirect}
}
