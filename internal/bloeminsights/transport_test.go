package bloeminsights

import "testing"

func TestClassifyViewerLeg(t *testing.T) {
	edge := func(proto string) *EdgeHop {
		return &EdgeHop{Name: "ams-1", Latitude: 52.37, Longitude: 4.89, ViewerProto: proto}
	}
	cases := []struct {
		name  string
		facts RequestFacts
		want  Transport
	}{
		{"edge with h3", RequestFacts{Proto: "HTTP/1.1", Edge: edge("h3")}, TransportUDP},
		{"edge with direct UDP", RequestFacts{Proto: "HTTP/1.1", Edge: edge("udp-direct")}, TransportUDP},
		{"edge with h2", RequestFacts{Proto: "HTTP/1.1", Edge: edge("h2")}, TransportTCP},
		{"edge with h1", RequestFacts{Proto: "HTTP/2.0", Edge: edge("h1")}, TransportTCP},
		{"edge behind Cloudflare", RequestFacts{Proto: "HTTP/1.1", Edge: edge("cloudflare")}, TransportCloudflare},
		{"edge without a viewer protocol, Cloudflare colo", RequestFacts{Proto: "HTTP/1.1", Edge: edge(""), CFColo: "AMS"}, TransportCloudflare},
		{"edge without a viewer protocol", RequestFacts{Proto: "HTTP/1.1", Edge: edge("")}, TransportUnknown},
		{"no edge, Cloudflare colo", RequestFacts{Proto: "HTTP/1.1", CFColo: "AMS"}, TransportCloudflare},
		{"no edge, HTTP/3", RequestFacts{Proto: "HTTP/3.0"}, TransportUDP},
		{"no edge, HTTP/2", RequestFacts{Proto: "HTTP/2.0"}, TransportTCP},
		{"no edge, HTTP/1.1", RequestFacts{Proto: "HTTP/1.1"}, TransportTCP},
		{"nothing known", RequestFacts{}, TransportUnknown},
	}
	for _, tc := range cases {
		if got := ClassifyViewerLeg(tc.facts); got != tc.want {
			t.Errorf("%s: ClassifyViewerLeg = %q, want %q", tc.name, got, tc.want)
		}
	}
}
