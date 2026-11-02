package auth

import (
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/permissioncatalog"
)

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

// The frozen v1 account body lists an enabled admin's role grant as every
// assignable permission (AssignablePermissions), and Bloem's catalog adds Live
// TV to that set.
func TestAssignablePermissionsListTheBloemCatalog(t *testing.T) {
	if got, want := AssignablePermissions(), permissioncatalog.Assignable(); !slices.Equal(got, want) {
		t.Fatalf("AssignablePermissions() = %v, want the permission catalog %v", got, want)
	}
}
