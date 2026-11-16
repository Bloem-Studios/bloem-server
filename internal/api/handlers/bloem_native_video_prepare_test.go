package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/downloadprepare"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

const preparationAttempt = "artifact-1-6f1b7a64-3a60-4f0c-9c1a-3d8e0f5b2c11"

func nativePreparationFixture(t *testing.T) (*NativeVideoDownloads, *models.MediaFile, *downloads.Download, *atomic.Bool, string) {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	source := filepath.Join(t.TempDir(), "movie.mp4")
	cmd := exec.Command(bin, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=128x96:r=24", "-t", "2", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-movflags", "+faststart", source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	file := &models.MediaFile{ID: 42, ContentID: "movie", MediaFolderID: 7, FilePath: "bloem-storage:movie", Container: "mp4", VideoTracks: []models.VideoTrack{{Codec: "h264"}}, Duration: 2}
	identity := nativestorage.PlaybackIdentity{FileID: 42, MediaFolderID: 7, ContentID: "movie", FilePath: file.FilePath, Reference: storagesource.PersistedRef{Revision: "version-1"}}
	revoked := new(atomic.Bool)
	open := func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		inner := &videoFileFixture{Reader: bytes.NewReader(body), id: identity, info: mediasource.Info{Name: "movie.mp4", Size: int64(len(body)), Revision: "version-1"}, closed: new(int)}
		return &nativeDeliverySource{NativeVideoFile: inner, revoked: revoked, reads: new(atomic.Int32), closes: new(atomic.Int32)}, nil
	}
	service := &NativeVideoService{Files: testPlaybackFileResolver{file: file}, Bindings: NewNativeVideoBindingStore(nil)}
	service.OpenDelegated = func(ctx context.Context, f *models.MediaFile, _ NativeVideoBinding) (NativeVideoFile, error) {
		return open(ctx, f)
	}
	requester := &downloads.Download{ID: "download-1", UserID: 7, ProfileID: "profile", MediaFileID: 42, ArtifactID: "artifact-1", Status: downloads.StatusPreparing}
	d := &NativeVideoDownloads{Service: service, Prepare: func(ctx context.Context, r *downloads.Download, artifactID string, f *models.MediaFile) (NativeVideoAuthority, NativeVideoFile, error) {
		opened, err := open(ctx, f)
		return NativeVideoAuthority{UserID: r.UserID, ProfileID: r.ProfileID, DownloadID: r.ID, ArtifactID: artifactID, ExpiresAt: time.Now().Add(time.Hour)}, opened, err
	}}
	return d, file, requester, revoked, bin
}

func preparationOpts(file *models.MediaFile, bin string) playback.TranscodeOpts {
	return playback.TranscodeOpts{InputPath: file.FilePath, SessionID: playback.DownloadPrepareLogSessionID("artifact-1"), FFmpegPath: bin, HWAccel: "none",
		TargetCodecVideo: "h264", TargetCodecAudio: "aac", AudioTrackIndex: -1, SubtitleTrackIndex: -1, TotalDuration: 2}
}

// A download preparation converts a native video through its binding, and
// ending the attempt withdraws the source.
func TestNativeVideoPreparationConvertsThroughItsBinding(t *testing.T) {
	d, file, requester, _, bin := nativePreparationFixture(t)
	ctx, end, err := d.BeginPreparation(t.Context(), "artifact-1", preparationAttempt, requester, file)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "prepared.mp4")
	if err := playback.PrepareFile(ctx, preparationOpts(file, bin), output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil || len(data) < 100 || string(data[4:8]) != "ftyp" {
		t.Fatalf("prepared file: %d bytes, %v", len(data), err)
	}
	end()
	if err := playback.PrepareFile(ctx, preparationOpts(file, bin), filepath.Join(t.TempDir(), "again.mp4")); err == nil {
		t.Fatal("prepared after the attempt ended")
	}
}

func TestNativeVideoPreparationStopsWhenTheSourceIsWithdrawn(t *testing.T) {
	d, file, requester, revoked, bin := nativePreparationFixture(t)
	ctx, end, err := d.BeginPreparation(t.Context(), "artifact-1", preparationAttempt, requester, file)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	revoked.Store(true)
	if err := playback.PrepareFile(ctx, preparationOpts(file, bin), filepath.Join(t.TempDir(), "prepared.mp4")); err == nil {
		t.Fatal("prepared from a withdrawn source")
	}
}

func TestNativeVideoPreparationRefusesOtherAttempts(t *testing.T) {
	d, file, requester, revoked, _ := nativePreparationFixture(t)
	if _, _, err := d.BeginPreparation(t.Context(), "artifact-2", preparationAttempt, requester, file); err == nil {
		t.Fatal("began for an artifact the download does not wait for")
	}
	if _, _, err := d.BeginPreparation(t.Context(), "artifact-1", "artifact-1", requester, file); err == nil {
		t.Fatal("began without an attempt UUID")
	}
	revoked.Store(true)
	if _, _, err := d.BeginPreparation(t.Context(), "artifact-1", preparationAttempt, requester, file); err == nil {
		t.Fatal("began on a source the viewer may not read")
	}
	revoked.Store(false)
	ctx, end, err := d.BeginPreparation(t.Context(), "artifact-1", preparationAttempt, requester, file)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	// The attempt prepares on the API server: a pooled preparer that would
	// send it to a transcode node is refused by the preparation's host.
	own, _ := json.Marshal(downloadprepare.Request{ArtifactID: preparationAttempt, InputPath: file.FilePath})
	if err := playback.RefuseContextRemoteStart(ctx, own); !errors.Is(err, playback.ErrContextInputStaysLocal) {
		t.Fatalf("remote preparation = %v, want it kept on the API server", err)
	}
}
