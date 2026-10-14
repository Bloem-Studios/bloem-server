package accesspolicy

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

func TestPreviewGroupIgnoresAccountOverrides(t *testing.T) {
	yes, four := true, 4
	user := &models.User{
		ID: 9, Role: models.RoleUser, LibraryIDs: []int{1, 2, 3}, Permissions: []string{"marker_edit"},
		DownloadAllowed: &yes, DownloadTranscodeAllowed: &yes, TranscodeAllowed: &yes,
		AudioTranscodeAllowed: &yes, RequestsAllowed: &yes, MaxStreams: &four, MaxTranscodes: &four,
	}
	group := &GroupPolicy{ID: 5, LibraryIDs: []int{2}, AllowedPermissions: []string{}, PreviewOnly: true}
	got := ApplyGroupPolicy(user, group)
	if !got.PreviewOnly {
		t.Fatal("preview group did not resolve to a preview policy")
	}
	if got.PlaybackAllowed || got.DownloadAllowed || got.DownloadTranscodeAllowed || got.TranscodeAllowed ||
		got.AudioTranscodeAllowed || got.RequestsAllowed || got.MaxStreams != 0 || got.MaxTranscodes != 0 {
		t.Fatalf("account overrides survived a preview group: %+v", got)
	}
	if len(got.LibraryIDs) != 1 || got.LibraryIDs[0] != 2 {
		t.Fatalf("libraries = %v, want only the template's [2]", got.LibraryIDs)
	}
	if len(got.Permissions) != 0 {
		t.Fatalf("permissions = %v, want none", got.Permissions)
	}
}

func TestAdminNeverResolvesToPreview(t *testing.T) {
	admin := &models.User{ID: 1, Role: models.RoleAdmin}
	if ApplyGroupPolicy(admin, &GroupPolicy{ID: 5, PreviewOnly: true}).PreviewOnly {
		t.Fatal("an admin resolved to a preview policy")
	}
}
