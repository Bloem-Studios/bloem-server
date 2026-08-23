package outbound_test

import (
	"context"
	"net/netip"
	"net/url"
	"testing"

	"github.com/Silo-Server/silo-server/internal/outbound"
)

type fixedResolver map[string][]netip.Addr

func (r fixedResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	if network != "ip" {
		panic("unexpected network " + network)
	}
	return append([]netip.Addr(nil), r[host]...), nil
}

func TestPublicPolicyClassifiesLiteralAddresses(t *testing.T) {
	client := outbound.NewClient(outbound.PublicHTTPPolicy())
	denied := []string{
		"0.1.2.3", "10.1.2.3", "100.64.0.1", "127.0.0.1",
		"169.254.1.1", "172.16.0.1", "192.0.0.1", "192.0.2.1",
		"192.88.99.1", "192.168.1.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "224.0.0.1", "255.255.255.255", "::1",
		"::192.168.0.1", "fc00::1", "fe80::1", "fec0::1", "2001:db8::1",
		"2002:7f00:1::1", "3fff::1", "5f00::1", "64:ff9b::7f00:1",
		"::ffff:127.0.0.1", "::ffff:192.168.0.5",
	}
	for _, address := range denied {
		target := &url.URL{Scheme: "http", Host: "[" + address + "]"}
		if netip.MustParseAddr(address).Is4() {
			target.Host = address
		}
		if err := client.Validate(t.Context(), target); err == nil {
			t.Errorf("Validate(%q) succeeded", address)
		}
	}

	for _, address := range []string{"1.1.1.1", "8.8.8.8", "151.101.1.69", "2606:4700::1111"} {
		target := &url.URL{Scheme: "http", Host: "[" + address + "]"}
		if netip.MustParseAddr(address).Is4() {
			target.Host = address
		}
		if err := client.Validate(t.Context(), target); err != nil {
			t.Errorf("Validate(%q): %v", address, err)
		}
	}
}

func TestPublicPolicyRejectsSpecialUseDestinations(t *testing.T) {
	client := outbound.NewClient(outbound.PublicHTTPPolicy(), outbound.WithResolver(fixedResolver{
		"mixed.example": {
			netip.MustParseAddr("93.184.216.34"),
			netip.MustParseAddr("127.0.0.1"),
		},
	}))

	cases := []string{
		"http://127.0.0.1/image.jpg",
		"http://[::1]/image.jpg",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::ffff:127.0.0.1]/image.jpg",
		"http://user:secret@example.test/image.jpg",
		"http://mixed.example/image.jpg",
	}
	for _, rawURL := range cases {
		t.Run(rawURL, func(t *testing.T) {
			_, err := client.Fetch(t.Context(), outbound.Request{
				URL:      rawURL,
				MaxBytes: 1024,
			})
			if err == nil {
				t.Fatalf("Fetch(%q) succeeded", rawURL)
			}
		})
	}
}
