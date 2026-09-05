package access

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

func TestLiveTVPermissionIndependentOfLibraries(t *testing.T) {
	for _, tc := range []struct {
		name        string
		permissions []string
		role        string
		primary     bool
		want        bool
	}{
		{"no grant", nil, "user", true, false},
		{"explicit grant", []string{"watch_live_tv"}, "user", true, true},
		{"other permission", []string{"marker_edit"}, "user", true, false},
		{"admin parent", nil, "admin", true, true},
		{"admin child", nil, "admin", false, false},
		{"admin child explicit", []string{"watch_live_tv"}, "admin", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := &models.User{Role: tc.role}
			profile := &userstore.Profile{IsPrimary: tc.primary}
			effective := EffectiveUserPolicy{Permissions: tc.permissions, LibraryIDs: []int{}}
			if got := LiveTVAllowed(user, profile, effective); got != tc.want {
				t.Fatalf("allowed=%v want %v", got, tc.want)
			}
		})
	}
}

func TestLiveTVGroupMaskDeniesAccountGrant(t *testing.T) {
	user := &models.User{Role: "user", Permissions: []string{"watch_live_tv"}}
	effective := ApplyGroupPolicy(user, &GroupPolicy{AllowedPermissions: []string{}})
	if LiveTVAllowed(user, nil, effective) {
		t.Fatal("group mask must deny grant")
	}
}
