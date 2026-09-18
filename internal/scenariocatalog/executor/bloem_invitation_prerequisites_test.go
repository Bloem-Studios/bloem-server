package executor

import "testing"

func TestBloemReseedRestoresInvitationPublicURL(t *testing.T) {
	env := New(t)
	if !env.HasDatabase() {
		t.Fatal(DatabaseEnv + " is required for invitation prerequisite coverage")
	}
	for range 2 {
		env.mustSetting("server.public_url", "")
		env.Reseed()
		got, err := env.settings.Get(t.Context(), "server.public_url")
		if err != nil {
			t.Fatal(err)
		}
		if got != publicURL {
			t.Fatalf("reseeded public URL = %q, want %q", got, publicURL)
		}
	}
}
