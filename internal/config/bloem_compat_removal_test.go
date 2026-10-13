package config

import "testing"

func TestRemovedCompatibilitySettingsDoNotAffectNativeConfig(t *testing.T) {
	_, err := LoadFromDB(map[string]string{
		"jellyfin_compat.enabled":      "not-a-bool",
		"jellyfin_compat.session_ttl":  "not-a-duration",
		"audiobookshelf_compat.listen": ":13378",
	})
	if err != nil {
		t.Fatalf("removed settings must be ignored: %v", err)
	}
}
