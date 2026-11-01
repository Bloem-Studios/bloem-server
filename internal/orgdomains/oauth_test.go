package orgdomains

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/clientip"
)

// The OAuth base follows the registered domain the request arrived on, in the
// server's scheme, and is empty anywhere else.
func TestOAuthBaseURLFollowsTheRequestDomain(t *testing.T) {
	d := newTestDirectory(testSnapshot())
	base := d.OAuthBaseURL(func(context.Context) string { return "https://lib.example.org" })
	var got string
	handler := WrapListener(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = base(r.Context())
	}), clientip.NewResolver(nil), d)

	for host, want := range map[string]string{
		"Main.Domain.com:443": "https://main.domain.com",
		"lib.example.org":     "https://lib.example.org",
		"10.0.0.5:8090":       "",
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if got != want {
			t.Errorf("host %s: base %q, want %q", host, got, want)
		}
	}

	plain := d.OAuthBaseURL(func(context.Context) string { return "http://lib.example.org:8090" })
	ctx := WithRequestDomain(context.Background(), "main.domain.com")
	if got := plain(ctx); got != "http://main.domain.com" {
		t.Fatalf("http server: %q", got)
	}
	if got := (*Directory)(nil).OAuthBaseURL(nil)(ctx); got != "" {
		t.Fatalf("nil directory: %q", got)
	}
}
