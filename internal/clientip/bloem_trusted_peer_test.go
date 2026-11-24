package clientip

import (
	"net/http/httptest"
	"testing"
)

func TestPeerTrusted(t *testing.T) {
	cidrs, err := ParseCIDRs(DefaultTrustedProxies)
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(cidrs)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.2:51000"
	if !resolver.PeerTrusted(req) {
		t.Fatal("a peer inside the default trusted proxies was not trusted")
	}
	req.RemoteAddr = "203.0.113.9:51000"
	if resolver.PeerTrusted(req) {
		t.Fatal("a public peer was trusted")
	}
	var none *Resolver
	if none.PeerTrusted(req) {
		t.Fatal("a nil resolver trusted a peer")
	}
}
