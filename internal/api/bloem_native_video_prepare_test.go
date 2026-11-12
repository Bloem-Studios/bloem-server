package api

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

// preparationFixture is a download waiting for a prepared copy of the
// fixture's native movie.
func (d *nativeVideoDelegationFixture) preparation() (*downloads.Download, *downloads.Download) {
	row := &downloads.Download{ID: "download-1", UserID: 7, ProfileID: "profile", MediaFileID: 11, ArtifactID: "artifact-1", Status: downloads.StatusPreparing}
	stored := *row
	d.base.policy.loadDownload = func(_ context.Context, id string) (*downloads.Download, error) {
		if id != stored.ID {
			return nil, downloads.ErrNotFound
		}
		row := stored
		return &row, nil
	}
	return row, &stored
}

// A download preparation reads the source under the waiting download's
// account and profile, with no credential, and keeps reading only while the
// download waits for this prepared file and the viewer may still read it.
func TestNativeVideoPreparationAuthorityFollowsTheDownload(t *testing.T) {
	for _, kind := range []string{"allowed", "cancelled", "relinked", "deleted", "account", "pin", "tenant", "viewer", "catalog", "source"} {
		t.Run(kind, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			f := d.base
			requester, stored := d.preparation()
			delegation, authority, err := nativeVideoPreparationAuthority(context.Background(), requester, "artifact-1", f.file, f.policy)
			if err != nil {
				t.Fatal(err)
			}
			if delegation.TokenType != nativeVideoDownloadPrepareTokenType || delegation.DownloadID != "download-1" || delegation.ArtifactID != "artifact-1" ||
				delegation.LoginSessionID != "" || delegation.PlaybackSessionID != "" || delegation.ProfileCredentialRevision != 2 || delegation.Tenant != f.tenant.tenant {
				t.Fatalf("delegation=%+v", delegation)
			}
			switch kind {
			case "cancelled":
				stored.Status = downloads.StatusCancelled
			case "relinked":
				stored.ArtifactID = "artifact-2"
			case "deleted":
				stored.ID = "gone"
			case "account":
				f.login.enabled = false
			case "pin":
				d.profile.PINRevision++
			case "tenant":
				f.tenant.tenant.SecurityRevision++
			case "viewer":
				f.viewer.scope.PlaybackAllowed = false
			case "catalog":
				f.fileErr = errors.New("hidden")
			case "source":
				f.sourceErr = errors.New("source hidden")
			}
			err = authority.AuthorizeSource(context.Background(), f.file, f.source)
			if (err == nil) != (kind == "allowed") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestNativeVideoPreparationAuthorityRefusesOtherDownloads(t *testing.T) {
	for _, kind := range []string{"other-file", "not-waiting", "other-artifact", "other-profile"} {
		t.Run(kind, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			f := d.base
			requester, stored := d.preparation()
			artifact := "artifact-1"
			switch kind {
			case "other-file":
				requester.MediaFileID = 12
			case "not-waiting":
				stored.Status = downloads.StatusReady
			case "other-artifact":
				artifact = "artifact-2"
			case "other-profile":
				stored.ProfileID = "other"
			}
			if _, _, err := nativeVideoPreparationAuthority(context.Background(), requester, artifact, f.file, f.policy); err == nil {
				t.Fatal("preparation authorized for a download not waiting for it")
			}
		})
	}
}

// Reads through a preparation's binding (on this server or a worker) recheck
// the download, with no playback session behind them.
func TestNativeVideoPreparationBindingRechecksTheDownload(t *testing.T) {
	d := newNativeVideoDelegationFixture(t)
	f := d.base
	requester, stored := d.preparation()
	delegation, _, err := nativeVideoPreparationAuthority(context.Background(), requester, "artifact-1", f.file, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	binding := handlers.NativeVideoBinding{SessionID: "6f1b7a64-3a60-4f0c-9c1a-3d8e0f5b2c11", UserID: 7, ProfileID: "profile", Authority: delegation,
		Identity: nativestorage.PlaybackIdentity{FileID: 11, MediaFolderID: 9, ContentID: "movie", FilePath: f.file.FilePath}}
	f.policy.loadBinding = func(context.Context, string, int) (handlers.NativeVideoBinding, error) { return binding, nil }
	authority, err := nativeVideoDelegatedAuthority(context.Background(), f.file, binding, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.AuthorizeSource(context.Background(), f.file, f.source); err != nil {
		t.Fatalf("bound preparation refused: %v", err)
	}
	stored.Status = downloads.StatusCancelled
	if err := authority.AuthorizeSource(context.Background(), f.file, f.source); err == nil {
		t.Fatal("bound preparation survived its download")
	}
	stored.Status = downloads.StatusPreparing
	f.denied = true
	if err := authority.AuthorizeSource(context.Background(), f.file, f.source); err == nil {
		t.Fatal("bound preparation survived revocation of its binding")
	}
}
