package config

// Bloem playback.header_authenticated_media_mode coverage moved out of Silo's
// admin_settings_test.go.

import (
	"testing"
)

func TestNormalizeHeaderAuthenticatedMediaMode(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
	}{
		{"", "disabled"},
		{" disabled ", "disabled"},
		{" single_or_affine ", "single_or_affine"},
	} {
		got, err := NormalizeAdminSetting("playback.header_authenticated_media_mode", test.raw)
		if err != nil {
			t.Fatalf("NormalizeAdminSetting(%q): %v", test.raw, err)
		}
		if got != test.want {
			t.Fatalf("NormalizeAdminSetting(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}

// Bloem row for Silo's TestNormalizeAdminSettingRejectsInvalidValues table.
func TestNormalizeHeaderAuthenticatedMediaModeRejectsUnknownMode(t *testing.T) {
	if _, err := NormalizeAdminSetting("playback.header_authenticated_media_mode", "always"); err == nil {
		t.Fatal(`NormalizeAdminSetting("playback.header_authenticated_media_mode", "always") accepted an invalid value`)
	}
}
