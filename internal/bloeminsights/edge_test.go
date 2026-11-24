package bloeminsights

import (
	"net/netip"
	"testing"
)

func TestParseEdgeHeader(t *testing.T) {
	hop, ok := ParseEdgeHeader("name=ams-1; lat=52.37; lon=4.89; viewer_proto=h3")
	if !ok {
		t.Fatal("a well-formed header was refused")
	}
	if hop != (EdgeHop{Name: "ams-1", Latitude: 52.37, Longitude: 4.89, ViewerProto: "h3"}) {
		t.Fatalf("hop = %+v", hop)
	}
	if _, ok := ParseEdgeHeader("name=ams-1; lat=200; lon=4.89; viewer_proto=h3"); ok {
		t.Fatal("latitude 200 was accepted")
	}
	if _, ok := ParseEdgeHeader("name=ams-1; lat=52.37; lon=-181; viewer_proto=h3"); ok {
		t.Fatal("longitude -181 was accepted")
	}
	if _, ok := ParseEdgeHeader("name=ams-1; lon=4.89"); ok {
		t.Fatal("a header without a latitude was accepted")
	}
	if _, ok := ParseEdgeHeader("lat=52.37; lon=4.89"); ok {
		t.Fatal("a header without a name was accepted")
	}
	if _, ok := ParseEdgeHeader(""); ok {
		t.Fatal("an empty header was accepted")
	}
	hop, ok = ParseEdgeHeader("name=ams-1; lat=52.37; lon=4.89; viewer_proto=h9")
	if !ok || hop.ViewerProto != "" {
		t.Fatalf("unknown viewer_proto: hop = %+v ok = %v, want accepted with no proto", hop, ok)
	}
	for _, proto := range []string{"h1", "h2", "h3", "udp-direct", "cloudflare"} {
		if hop, ok := ParseEdgeHeader("name=e; lat=1; lon=2; viewer_proto=" + proto); !ok || hop.ViewerProto != proto {
			t.Fatalf("viewer_proto %s: hop = %+v ok = %v", proto, hop, ok)
		}
	}
}

func TestCloudflareColo(t *testing.T) {
	colo, ok := CloudflareColo("8c2b1a2f3e4d5a6b-AMS")
	if !ok || colo != "AMS" {
		t.Fatalf("colo = %q ok = %v, want AMS", colo, ok)
	}
	if colo, ok := CloudflareColo("8c2b1a2f3e4d5a6b-ams"); !ok || colo != "AMS" {
		t.Fatalf("lower-case colo = %q ok = %v, want AMS", colo, ok)
	}
	for _, ray := range []string{"", "8c2b1a2f3e4d5a6b", "8c2b1a2f3e4d5a6b-", "x-A1", "x-TOOLONG"} {
		if colo, ok := CloudflareColo(ray); ok {
			t.Fatalf("CloudflareColo(%q) = %q, want refused", ray, colo)
		}
	}
	city, lat, lon, ok := ColoLocation("AMS")
	if !ok || city != "Amsterdam" || lat < 52 || lat > 53 || lon < 4 || lon > 5 {
		t.Fatalf("AMS = %q %v %v %v, want Amsterdam", city, lat, lon, ok)
	}
	if _, _, _, ok := ColoLocation("ZZZ"); ok {
		t.Fatal("an unknown colo has a location")
	}
}

func TestIsCloudflareAddress(t *testing.T) {
	if !IsCloudflareAddress(netip.MustParseAddr("162.158.1.1")) || !IsCloudflareAddress(netip.MustParseAddr("2606:4700::1")) {
		t.Fatal("a Cloudflare egress address was not recognized")
	}
	if IsCloudflareAddress(netip.MustParseAddr("203.0.113.9")) {
		t.Fatal("a documentation address was taken for Cloudflare")
	}
}
