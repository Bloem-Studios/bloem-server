package bloeminsights

import "time"

// RequestFacts are what one media request tells about its stream's
// connection: the protocol it reached this server with, the edge that
// forwarded it and the Cloudflare colo it passed, both only under trust.
type RequestFacts struct {
	Proto  string    `json:"proto,omitempty"`
	Edge   *EdgeHop  `json:"edge,omitempty"`
	CFColo string    `json:"cf_colo,omitempty"`
	At     time.Time `json:"at"`
}
