package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
)

func TestAudiobookCredentialBundleBindsOnlyKnownSecrets(t *testing.T) {
	config := map[string]any{"qbittorrent_url": "https://Client.Example:443/prefix/", "ai_base_url": "https://ai.example/v1/"}
	value, err := audiobookCredentialBundle(map[string]string{"qbittorrent_password": "synthetic-pass", "ai_api_key": "synthetic-key"}, config)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(value[len("abk1."):])
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Password string            `json:"qbittorrent_password"`
		Key      string            `json:"ai_api_key"`
		Bind     map[string]string `json:"bind"`
	}
	if err = json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Password != "synthetic-pass" || bundle.Key != "synthetic-key" || bundle.Bind["qbittorrent_url"] != audiobookURLFingerprint("https://client.example/prefix") || bundle.Bind["ai_base_url"] != audiobookURLFingerprint("https://ai.example/v1") {
		t.Fatalf("credential destination binding failed")
	}
	for _, credentials := range []map[string]string{{"unknown": "x"}, {"qbittorrent_password": "x\n"}, {"nzbget_password": "x"}} {
		if _, err = audiobookCredentialBundle(credentials, config); !errors.Is(err, bookrequests.ErrInvalid) {
			t.Fatalf("invalid bundle accepted: %v", err)
		}
	}
	for _, raw := range []string{"https://user:pass@example.test", "https://example.test?token=x", "https://example.test#token", "file:///tmp/file", "https://%"} {
		if audiobookURLFingerprint(raw) != "" {
			t.Fatal("unsafe destination accepted")
		}
	}
}
