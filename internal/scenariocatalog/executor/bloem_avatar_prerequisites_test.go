package executor

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
)

func TestBloemAvatarPrerequisiteSelection(t *testing.T) {
	row := scenariocatalog.Row{Method: "PUT", Path: "/api/v1/profiles/{id}/avatar"}
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"avatar_upload.raw", true},
		{"avatar_upload.missing_file", true},
		{"avatar_upload.not_multipart", true},
		{"avatar_upload.not_found", true},
		{"avatar_upload.shape", true},
		{"avatar_upload.any_profile", true},
		{"avatar_upload.other_account_profile", true},
		{"avatar_upload.other_account_path", true},
		{"avatar_upload.no_token", true},
		{"avatar_upload.typed_nil_panic", false},
		{"avatar_upload.meaning", false},
		{"unrecognized", false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			s := scenariocatalog.Scenario{ID: tc.id}
			if got := bloemAvatarStorageRequired(row, s); got != tc.want {
				t.Fatalf("local storage prerequisite = %v, want %v", got, tc.want)
			}
			s.Requires = []string{"database_unavailable"}
			if bloemAvatarStorageRequired(row, s) {
				t.Fatal("outage scenario gained a live storage fixture")
			}
		})
	}
	if bloemAvatarStorageRequired(scenariocatalog.Row{Method: "GET", Path: row.Path}, scenariocatalog.Scenario{ID: "avatar_upload.raw"}) {
		t.Fatal("non-upload row gained an upload prerequisite")
	}
}
