package config

import "testing"

func TestEbookRequestOptInDefaultAndValidation(t *testing.T) {
	const key = "bloem.requests.ebooks_enabled"
	if got := EffectiveAdminSettings(nil)[key]; got != "false" {
		t.Errorf("ebook acquisition must default off: %q", got)
	}
	for _, raw := range []string{"true", " TRUE ", "1"} {
		got, err := NormalizeAdminSetting(key, raw)
		if err != nil || got != "true" {
			t.Errorf("normalize %q: %q %v", raw, got, err)
		}
	}
	if _, err := NormalizeAdminSetting(key, "enabled"); err == nil {
		t.Error("invalid ebook opt-in accepted")
	}
}
