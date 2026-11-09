package clientip

// Bloem-owned tests for RequestHostOf (bloem_request_host.go).

import (
	"net/http/httptest"
	"testing"
)

// RequestHostOf follows Middleware's rule: only a trusted proxy's single
// X-Forwarded-Host names the host, so a client cannot pick an organization's
// domain with a forwarded header.
func TestRequestHostOfTrustsOnlyAProxysSingleHost(t *testing.T) {
	trusted, err := ParseCIDRs("192.0.2.1/32")
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(trusted)
	for _, tc := range []struct {
		name     string
		resolver *Resolver
		peer     string
		hosts    []string
		want     string
	}{
		{"trusted proxy", resolver, "192.0.2.1:1234", []string{"tenant.example.test"}, "tenant.example.test"},
		{"untrusted client", resolver, "198.51.100.7:1234", []string{"tenant.example.test"}, "server.example.test"},
		{"no forwarded host", resolver, "192.0.2.1:1234", nil, "server.example.test"},
		{"blank forwarded host", resolver, "192.0.2.1:1234", []string{"  "}, "server.example.test"},
		{"list", resolver, "192.0.2.1:1234", []string{"a.example.test, b.example.test"}, "server.example.test"},
		{"two headers", resolver, "192.0.2.1:1234", []string{"a.example.test", "b.example.test"}, "server.example.test"},
		{"nil resolver", nil, "192.0.2.1:1234", []string{"tenant.example.test"}, "server.example.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://server.example.test/", nil)
			r.RemoteAddr = tc.peer
			for _, host := range tc.hosts {
				r.Header.Add("X-Forwarded-Host", host)
			}
			if got := tc.resolver.RequestHostOf(r); got != tc.want {
				t.Fatalf("RequestHostOf = %q, want %q", got, tc.want)
			}
		})
	}
}
