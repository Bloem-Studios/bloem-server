package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
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

func TestRecomposeAudiobookCredentialsKeepsSecretsOnlyForUnchangedURLs(t *testing.T) {
	saved, err := audiobookCredentialBundle(map[string]string{"qbittorrent_password": "saved-qb", "ai_api_key": "saved-ai", "abook_email": "reader@example.test", "abook_password": "saved-abook"}, map[string]any{"qbittorrent_url": "https://client.example.test/qb", "ai_base_url": "https://ai.example.test/v1", "abook_base_url": "https://abook.example.test/book"})
	if err != nil {
		t.Fatal(err)
	}
	decoded := func(value string) (map[string]string, map[string]string) {
		t.Helper()
		secrets, bind, err := decodeAudiobookCredentials(value)
		if err != nil {
			t.Fatal(err)
		}
		return secrets, bind
	}

	// Equivalent spellings of the same URLs keep every saved secret, and a
	// rotated one replaces only itself.
	same := map[string]any{"qbittorrent_url": "https://CLIENT.example.test:443/qb/", "ai_base_url": "https://ai.example.test/v1", "abook_base_url": "https://abook.example.test/book"}
	value, err := recomposeAudiobookCredentials(map[string]string{"qbittorrent_password": "rotated-qb", "ai_api_key": ""}, saved, same)
	if err != nil {
		t.Fatal(err)
	}
	secrets, bind := decoded(value)
	if secrets["qbittorrent_password"] != "rotated-qb" || secrets["ai_api_key"] != "saved-ai" || secrets["abook_password"] != "saved-abook" || bind["qbittorrent_url"] != audiobookURLFingerprint("https://client.example.test/qb") {
		t.Fatalf("rotation did not keep or replace the right secrets: %v", bind)
	}

	// A changed URL never inherits its saved secret.
	moved := map[string]any{"qbittorrent_url": "https://attacker.example.test/qb", "ai_base_url": "https://ai.example.test/v1", "abook_base_url": "https://abook.example.test/book"}
	if _, err = recomposeAudiobookCredentials(nil, saved, moved); !errors.Is(err, bookrequests.ErrInvalid) || !strings.Contains(err.Error(), "qBittorrent password") {
		t.Fatalf("changed URL kept its secret: %v", err)
	}
	value, err = recomposeAudiobookCredentials(map[string]string{"qbittorrent_password": "typed-for-new-host"}, saved, moved)
	if err != nil {
		t.Fatal(err)
	}
	if secrets, bind = decoded(value); secrets["qbittorrent_password"] != "typed-for-new-host" || bind["qbittorrent_url"] != audiobookURLFingerprint("https://attacker.example.test/qb") {
		t.Fatal("re-entered secret was not bound to the new URL")
	}

	// Both abook credentials follow their URL together.
	abookMoved := map[string]any{"qbittorrent_url": "https://client.example.test/qb", "ai_base_url": "https://ai.example.test/v1", "abook_base_url": "https://other-abook.example.test/book"}
	if _, err = recomposeAudiobookCredentials(map[string]string{"abook_password": "only-one"}, saved, abookMoved); !errors.Is(err, bookrequests.ErrInvalid) {
		t.Fatalf("abook email moved without re-entry: %v", err)
	}

	// Clearing a URL drops the secrets bound to it.
	cleared := map[string]any{"qbittorrent_url": "", "ai_base_url": "https://ai.example.test/v1", "abook_base_url": "https://abook.example.test/book"}
	value, err = recomposeAudiobookCredentials(nil, saved, cleared)
	if err != nil {
		t.Fatal(err)
	}
	if secrets, bind = decoded(value); secrets["qbittorrent_password"] != "" || bind["qbittorrent_url"] != "" || secrets["ai_api_key"] != "saved-ai" {
		t.Fatal("cleared URL kept its secret")
	}

	// An unreadable saved bundle keeps nothing.
	if value, err = recomposeAudiobookCredentials(nil, "not-a-bundle", same); err != nil {
		t.Fatal(err)
	}
	if secrets, _ = decoded(value); len(secrets) != 0 {
		t.Fatal("unreadable bundle kept secrets")
	}
	for _, updates := range []map[string]string{{"unknown": "x"}, {"ai_api_key": "x\n"}} {
		if _, err = recomposeAudiobookCredentials(updates, saved, same); !errors.Is(err, bookrequests.ErrInvalid) {
			t.Fatalf("invalid update accepted: %v", err)
		}
	}
}

func TestConnectionTestMessagesWithholdCredentials(t *testing.T) {
	for _, tc := range []struct {
		ok      bool
		message string
		want    string
	}{
		{true, "", "Connection test passed."},
		{false, "  ", "Connection test failed."},
		{true, "Bookwarehouse monitoring API authenticated.", "Bookwarehouse monitoring API authenticated."},
		{false, "rejected key synthetic-secret-key", "The plugin's message was withheld because it contained a saved credential."},
		{false, "line\none\x00two", "line one two"},
	} {
		if got := connectionTestMessage(tc.ok, tc.message, []string{"synthetic-secret-key", ""}); got != tc.want {
			t.Errorf("connectionTestMessage(%v, %q) = %q, want %q", tc.ok, tc.message, got, tc.want)
		}
	}
	long := connectionTestMessage(true, strings.Repeat("é", maxConnectionTestMessage+10), nil)
	if len([]rune(long)) != maxConnectionTestMessage+1 {
		t.Fatalf("long message length = %d", len([]rune(long)))
	}

	// Collapsing whitespace for display must not let a credential that
	// contains whitespace through, in either direction.
	const withheld = "The plugin's message was withheld because it contained a saved credential."
	for _, tc := range []struct{ message, secret string }{
		{"login failed for password correct  horse\tbattery", "correct  horse\tbattery"},
		{"login failed for password correct horse battery", "correct  horse\tbattery"},
		{"login failed for password correct\n\nhorse battery", "correct horse battery"},
	} {
		if got := pluginMessage(tc.message, []string{tc.secret}); got != withheld {
			t.Errorf("pluginMessage(%q, %q) = %q", tc.message, tc.secret, got)
		}
	}
	if got := pluginMessage("credential check failed", []string{"\x01"}); got != "credential check failed" {
		t.Errorf("a control-only credential withheld every message: %q", got)
	}

	// The audiobook composite decodes to every credential it carries, so an
	// echo of the composite is withheld like an echo of a credential.
	composite, err := audiobookCredentialBundle(map[string]string{"qbittorrent_password": "synthetic-pass"}, map[string]any{"qbittorrent_url": "https://client.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if got := connectionTestMessage(false, "cannot parse "+composite, []string{"synthetic-pass", composite}); got != withheld {
		t.Fatalf("composite echo shown: %q", got)
	}
}
