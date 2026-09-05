package auth

import "testing"

func TestLiveTVPermissionAssignableButNotDefault(t *testing.T) {
	if _, err := NormalizePermissions([]string{"watch_live_tv"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range DefaultUserPermissions() {
		if p == "watch_live_tv" {
			t.Fatal("live tv must be explicitly granted")
		}
	}
}
