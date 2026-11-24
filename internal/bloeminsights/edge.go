// Package bloeminsights records where Bloem's streams come from and how they
// are connected, for the Admin apps' stream insights.
package bloeminsights

import (
	"net/netip"
	"strconv"
	"strings"
)

// EdgeHeader is the header a Bloem edge adds to every request it forwards,
// naming itself, where it is, and the protocol the viewer used to reach it:
//
//	X-Bloem-Edge: name=<label>; lat=<deg>; lon=<deg>; viewer_proto=<h1|h2|h3|udp-direct|cloudflare>
//
// It counts only from a trusted proxy peer: any viewer can send it.
const EdgeHeader = "X-Bloem-Edge"

// maxEdgeNameLen bounds the label an edge gives itself.
const maxEdgeNameLen = 64

// EdgeHop is one edge a request passed through.
type EdgeHop struct {
	Name        string  `json:"name"`
	Latitude    float64 `json:"lat"`
	Longitude   float64 `json:"lon"`
	ViewerProto string  `json:"viewer_proto,omitempty"`
}

var edgeViewerProtos = map[string]bool{"h1": true, "h2": true, "h3": true, "udp-direct": true, "cloudflare": true}

// ParseEdgeHeader reads an EdgeHeader value. It needs a name and a latitude
// and longitude in range; an unknown viewer_proto is kept as "".
func ParseEdgeHeader(v string) (EdgeHop, bool) {
	var hop EdgeHop
	var haveLat, haveLon bool
	for part := range strings.SplitSeq(v, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "name":
			hop.Name = value
		case "lat":
			lat, err := strconv.ParseFloat(value, 64)
			if err != nil || lat < -90 || lat > 90 {
				return EdgeHop{}, false
			}
			hop.Latitude, haveLat = lat, true
		case "lon":
			lon, err := strconv.ParseFloat(value, 64)
			if err != nil || lon < -180 || lon > 180 {
				return EdgeHop{}, false
			}
			hop.Longitude, haveLon = lon, true
		case "viewer_proto":
			if edgeViewerProtos[value] {
				hop.ViewerProto = value
			}
		}
	}
	if !validEdgeName(hop.Name) || !haveLat || !haveLon {
		return EdgeHop{}, false
	}
	return hop, true
}

func validEdgeName(name string) bool {
	if name == "" || len(name) > maxEdgeNameLen {
		return false
	}
	for _, r := range name {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// cloudflarePrefixes are Cloudflare's published edge egress ranges, the list
// Proxy Manager's analytics and the Bloem edges trust. CF-Ray is evidence of
// a Cloudflare hop only from one of them, or from a trusted proxy.
var cloudflarePrefixes = func() []netip.Prefix {
	var prefixes []netip.Prefix
	for _, cidr := range []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
		"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
	} {
		prefixes = append(prefixes, netip.MustParsePrefix(cidr))
	}
	return prefixes
}()

// IsCloudflareAddress reports whether address is a Cloudflare egress address.
func IsCloudflareAddress(address netip.Addr) bool {
	address = address.Unmap()
	for _, prefix := range cloudflarePrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// CloudflareColo returns the colo code at the end of a CF-Ray value
// ("8c2b1a2f3e4d5a6b-AMS" gives "AMS"), upper case.
func CloudflareColo(cfRay string) (string, bool) {
	i := strings.LastIndexByte(cfRay, '-')
	if i < 0 {
		return "", false
	}
	colo := strings.ToUpper(strings.TrimSpace(cfRay[i+1:]))
	if len(colo) != 3 {
		return "", false
	}
	for _, r := range colo {
		if r < 'A' || r > 'Z' {
			return "", false
		}
	}
	return colo, true
}

// ColoLocation places a known Cloudflare colo at its airport.
func ColoLocation(code string) (city string, lat, lon float64, ok bool) {
	location, ok := cfColoLocations[strings.ToUpper(code)]
	if !ok {
		return "", 0, 0, false
	}
	return location.City, location.Lat, location.Lon, true
}

// coloLocation is one entry of the generated cfColoLocations table.
type coloLocation struct {
	City string
	Lat  float64
	Lon  float64
}
