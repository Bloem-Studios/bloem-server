package outbound

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

type resolverMap map[string][]netip.Addr

func (r resolverMap) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r[host]...), nil
}

func TestClientDialsTheValidatedAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("image"))
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(serverURL.Host)
	if err != nil {
		t.Fatal(err)
	}

	var dialed string
	client := NewClient(PublicHTTPPolicy(),
		WithResolver(resolverMap{"public.example": {netip.MustParseAddr("93.184.216.34")}}),
		func(options *clientOptions) {
			options.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
				dialed = address
				return (&net.Dialer{}).DialContext(ctx, network, serverURL.Host)
			}
		},
	)
	response, err := client.Fetch(t.Context(), Request{
		URL:      fmt.Sprintf("http://public.example:%s/image", port),
		MaxBytes: 32,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(response.Body) != "image" {
		t.Fatalf("body = %q, want image", response.Body)
	}
	if dialed != net.JoinHostPort("93.184.216.34", port) {
		t.Fatalf("dialed %q, want validated address", dialed)
	}
}

func TestClientRevalidatesRedirectDestinations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/start" {
			http.Redirect(response, request, "http://private.example/final", http.StatusFound)
			return
		}
		t.Fatal("private redirect reached the server")
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(serverURL.Host)
	if err != nil {
		t.Fatal(err)
	}

	client := NewClient(PublicHTTPPolicy(),
		WithResolver(resolverMap{
			"public.example":  {netip.MustParseAddr("93.184.216.34")},
			"private.example": {netip.MustParseAddr("10.0.0.7")},
		}),
		func(options *clientOptions) {
			options.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, serverURL.Host)
			}
		},
	)
	_, err = client.Fetch(t.Context(), Request{
		URL:      fmt.Sprintf("http://public.example:%s/start", port),
		MaxBytes: 32,
	})
	if err == nil || !strings.Contains(err.Error(), "private or special-use") {
		t.Fatalf("Fetch redirect error = %v, want private-destination rejection", err)
	}
}

func TestFetchRejectsStreamedBodyPastLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Length", "")
		_, _ = response.Write([]byte("12345"))
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	client := NewClient(Policy{
		AllowedSchemes: map[string]struct{}{"http": {}},
		AllowPrivate:   true,
		MaxRedirects:   0,
	})
	_, err = client.Fetch(t.Context(), Request{URL: serverURL.String(), MaxBytes: 4})
	if err == nil || !strings.Contains(err.Error(), "exceeds 4 byte limit") {
		t.Fatalf("Fetch error = %v, want streamed size rejection", err)
	}
}
