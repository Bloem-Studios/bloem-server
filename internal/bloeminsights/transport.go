package bloeminsights

import "strings"

// Transport is how a stream's viewer leg is carried.
type Transport string

const (
	TransportUDP        Transport = "udp"
	TransportTCP        Transport = "tcp"
	TransportCloudflare Transport = "cloudflare"
	TransportUnknown    Transport = "unknown"
)

// Transports lists every class, in display order.
var Transports = []Transport{TransportUDP, TransportTCP, TransportCloudflare, TransportUnknown}

// ClassifyViewerLeg classifies the leg between the viewer and the first hop
// Bloem controls. Behind an edge, the edge's viewer_proto says it; without
// one, a Cloudflare colo means the viewer reached Cloudflare, and otherwise
// the request's own protocol is the viewer's.
func ClassifyViewerLeg(f RequestFacts) Transport {
	if f.Edge != nil {
		switch f.Edge.ViewerProto {
		case "h3", "udp-direct":
			return TransportUDP
		case "h1", "h2":
			return TransportTCP
		case "cloudflare":
			return TransportCloudflare
		}
		if f.CFColo != "" {
			return TransportCloudflare
		}
		return TransportUnknown
	}
	if f.CFColo != "" {
		return TransportCloudflare
	}
	switch {
	case strings.HasPrefix(f.Proto, "HTTP/3"):
		return TransportUDP
	case strings.HasPrefix(f.Proto, "HTTP/1"), strings.HasPrefix(f.Proto, "HTTP/2"):
		return TransportTCP
	}
	return TransportUnknown
}
