package config

import "testing"

func TestInitialPlaybackAPIOrigin(t *testing.T) {
	for _, raw := range []string{"", "https://api.example.test", "http://api.example.test:8080/"} {
		if _, err := initialPlaybackAPIOrigin(raw); err != nil {
			t.Fatalf("origin %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"api.example.test", "//api.example.test", "ftp://api.example.test", "https://user:secret@api.example.test", "https://api.example.test/prefix", "https://api.example.test/?q=x", "https://api.example.test/?", "https://api.example.test/#", "https://api.example.test/%2f", " https://api.example.test"} {
		if _, err := initialPlaybackAPIOrigin(raw); err == nil {
			t.Fatalf("invalid origin accepted: %q", raw)
		}
	}
}
