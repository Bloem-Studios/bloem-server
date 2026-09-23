package imagecache

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/outbound"
)

// bloemOutboundImageClient returns Bloem's shared SSRF-guarded outbound
// client for artwork downloads. It validates every resolved address (not just
// the first public one) and every redirect against the outbound public-HTTP
// policy, which denies a superset of the ranges Silo's secureImageDialContext
// rejects. Returning nil falls back to Silo's client.
func bloemOutboundImageClient() *http.Client {
	return outbound.NewClient(
		outbound.PublicHTTPPolicy(),
		outbound.WithTimeout(downloadTimeout),
	).HTTPClient()
}
