package handlers

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
)

// A new household login's password follows the server's password policy, as
// a password an administrator sets in Silo does.
func TestBloemHouseholdLoginPasswordFollowsThePolicy(t *testing.T) {
	t.Parallel()
	h := &AdminHandler{SettingsRepo: &fakeServerSettingsStore{values: map[string]string{
		config.AuthPasswordMinLengthSettingKey:        "12",
		config.AuthPasswordRequireUppercaseSettingKey: "true",
	}}}
	login := BloemHouseholdLoginCreateRequest{Username: "kid", ProfileName: "Kid", MaxStreams: 1, MaxProfiles: 1}
	for _, tt := range []struct{ password, rule string }{
		{"abcdefghij", "Use at least 12 characters."},
		{"abcdefghijkl", "Include at least one capital letter."},
	} {
		login.Password = tt.password
		err := h.validateBloemHouseholdLogin(t.Context(), login)
		if message, ok := auth.PasswordRuleMessage(err); !ok || message != tt.rule {
			t.Fatalf("password %q: error = %v; want the rule %q", tt.password, err, tt.rule)
		}
	}
	login.Password = "Abcdefghijkl"
	if err := h.validateBloemHouseholdLogin(t.Context(), login); err != nil {
		t.Fatalf("a password that meets the policy: %v", err)
	}
}
