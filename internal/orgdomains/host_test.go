package orgdomains

import (
	"errors"
	"testing"
)

func TestNormalizeHost(t *testing.T) {
	got, err := NormalizeHost("Main.Domain.com")
	if err != nil || got != "main.domain.com" {
		t.Fatalf("NormalizeHost(Main.Domain.com) = %q, %v; want main.domain.com", got, err)
	}
	for _, raw := range []string{"http://a.com", "a.com:443", "a.com/x", "localhost", "", "a..com", "-a.com", "a_b.com"} {
		if got, err := NormalizeHost(raw); !errors.Is(err, ErrInvalidHost) {
			t.Errorf("NormalizeHost(%q) = %q, %v; want ErrInvalidHost", raw, got, err)
		}
	}
}

func TestRequestHostKey(t *testing.T) {
	for raw, want := range map[string]string{
		"Main.Domain.com:443": "main.domain.com",
		"main.domain.com":     "main.domain.com",
		"[::1]:8090":          "::1",
		"":                    "",
	} {
		if got := requestHostKey(raw); got != want {
			t.Errorf("requestHostKey(%q) = %q, want %q", raw, got, want)
		}
	}
}
