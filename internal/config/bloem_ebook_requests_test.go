package config

import "testing"

func TestBloemOptInSettingsDefaultOffAndValidate(t *testing.T) {
	for _, key := range []string{
		"bloem.requests.ebooks_enabled",
		"bloem.auth.profile_passwords_enabled",
	} {
		t.Run(key, func(t *testing.T) {
			if got := EffectiveAdminSettings(nil)[key]; got != "false" {
				t.Errorf("must default off: %q", got)
			}
			for _, raw := range []string{"true", " TRUE ", "1"} {
				got, err := NormalizeAdminSetting(key, raw)
				if err != nil || got != "true" {
					t.Errorf("normalize %q: %q %v", raw, got, err)
				}
			}
			if _, err := NormalizeAdminSetting(key, "enabled"); err == nil {
				t.Error("invalid opt-in value accepted")
			}
		})
	}
}
