package accesspolicy

import (
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

func TestBloemAdminPolicyUsesFullAccountDefaults(t *testing.T) {
	got := ApplyGroupPolicy(&models.User{Role: models.RoleAdmin}, nil)
	if !got.DownloadTranscodeAllowed || !got.PlaybackAllowed || !got.DownloadAllowed || !got.TranscodeAllowed {
		t.Fatalf("admin account defaults should allow playback and download preparation: %#v", got)
	}
}

func TestBloemAdminPolicyIgnoresStaleGroupAndRetainsAccountOverrides(t *testing.T) {
	disabled := false
	limit := 2
	quality := "1080p"
	user := &models.User{
		Role:                     models.RoleAdmin,
		LibraryIDs:               []int{9},
		DownloadTranscodeAllowed: &disabled,
		MaxStreams:               &limit,
		MaxProfiles:              3,
		MaxPlaybackQuality:       &quality,
		Permissions:              []string{"watch"},
	}
	group := &GroupPolicy{
		LibraryIDs:         []int{7},
		MaxStreams:         1,
		MaxProfiles:        1,
		AllowedPermissions: []string{},
	}
	got := ApplyGroupPolicy(user, group)
	if got.DownloadTranscodeAllowed || got.MaxStreams != 2 || got.MaxProfiles != 3 || got.MaxPlaybackQuality != "1080p" || !reflect.DeepEqual(got.LibraryIDs, []int{9}) || !reflect.DeepEqual(got.Permissions, []string{"watch"}) || !got.PlaybackAllowed {
		t.Fatalf("stale group changed an admin account's explicit policy: %#v", got)
	}
}

func TestBloemRegularPolicyStillUsesOrganizationRestrictions(t *testing.T) {
	user := &models.User{Role: models.RoleUser, Permissions: []string{"watch", "download"}}
	group := &GroupPolicy{
		LibraryIDs:               []int{7},
		PlaybackAllowed:          true,
		DownloadAllowed:          false,
		DownloadTranscodeAllowed: false,
		MaxStreams:               1,
		AllowedPermissions:       []string{"watch"},
	}
	got := ApplyGroupPolicy(user, group)
	if got.DownloadAllowed || got.DownloadTranscodeAllowed || got.MaxStreams != 1 || !reflect.DeepEqual(got.LibraryIDs, []int{7}) || !reflect.DeepEqual(got.Permissions, []string{"watch"}) {
		t.Fatalf("regular account lost its organization restrictions: %#v", got)
	}
}
