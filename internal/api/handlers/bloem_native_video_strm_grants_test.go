package handlers

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// localStrmGrantFixture binds a session to a local `.strm` item: its catalog
// path is the `.strm` file, its identity has no storage location, and its
// revision is the URL's digest.
func localStrmGrantFixture(t *testing.T, path string) (*NativeVideoService, *playback.Session, transcodenode.TranscodeStartRequest) {
	t.Helper()
	client := nativeVideoGrantRedis(t)
	s, file, session, identity, closed := nativeVideoFixture(t)
	file.FilePath, file.Container = path, "mkv"
	*identity = nativestorage.PlaybackIdentity{FileID: file.ID, MediaFolderID: file.MediaFolderID, ContentID: file.ContentID, FilePath: path,
		Reference: storagesource.PersistedRef{Revision: "url-sha256:" + uuid.NewString(), LogicalPath: path}}
	s.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		return &videoFileFixture{Reader: bytes.NewReader([]byte("0123456789")), id: *identity, info: mediasource.Info{Name: "Harbor Lights (2019).strm", LogicalPath: path, Size: 10, Revision: identity.Reference.Revision}, closed: closed}, nil
	}
	s.Bindings = NewNativeVideoBindingStore(client)
	s.Deny = playback.NewStreamDeny(client)
	s.Delegation = func(context.Context) (NativeVideoAuthority, error) {
		return NativeVideoAuthority{UserID: session.UserID, ProfileID: session.ProfileID, TokenType: "stream", PlaybackSessionID: session.ID, ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
	}
	s.OpenDelegated = func(ctx context.Context, _ *models.MediaFile, binding NativeVideoBinding) (NativeVideoFile, error) {
		if binding.SessionID != session.ID || binding.Identity != *identity {
			return nil, storagesource.ErrReferenceConflict
		}
		return s.Open(ctx, file)
	}
	if err := s.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	transport := uuid.NewString()
	start := transcodenode.TranscodeStartRequest{SessionID: transport, InputPath: path, SourceVideoCodec: "h264", TargetCodecVideo: "h264", TargetCodecAudio: "copy",
		TargetResolution: "720p", TargetBitrateKbps: 2000, SegmentDuration: 6, AudioTrackIndex: -1, SubtitleTrackIndex: -1, TotalDuration: 10, HWAccel: "none", RequireReady: true}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = NewNativeVideoWorkerGrantStore(client).RevokeTransport(ctx, transport)
		key, _ := nativeVideoBindingKey(session.ID, file.ID)
		_ = client.Del(ctx, key, "bloem:native-video-session:"+session.ID, playback.StreamDenyKey(session.ID)).Err()
	})
	return s, session, start
}

// A local `.strm` item has no storage location, so its worker grant is bound
// to its catalog path. The worker receives that path and the grant, never the
// URL, and only the exact prepared start resolves.
func TestNativeVideoWorkerGrantAdmitsLocalStrmItems(t *testing.T) {
	const path = "/media/movies/Harbor Lights (2019)/Harbor Lights (2019).strm"
	s, session, start := localStrmGrantFixture(t, path)
	store := NewNativeVideoWorkerGrantStore(nativeVideoGrantRedis(t))
	if err := store.Prepare(t.Context(), 7, s, session, start); err != nil {
		t.Fatalf("Prepare for a local .strm = %v", err)
	}
	request := transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: path, Start: &start}
	grant, err := store.Resolve(t.Context(), request, s)
	if err != nil || grant == nil || grant.InputPath != path || grant.PlaybackSessionID != session.ID || grant.InputFormat != "matroska" {
		t.Fatalf("Resolve for a local .strm = %+v, %v", grant, err)
	}
	other := request
	other.InputPath = "/media/movies/Other (2020)/Other (2020).strm"
	if _, err := store.Resolve(t.Context(), other, s); err == nil {
		t.Fatal("a grant for one .strm item admitted another path")
	}
}

// A location-free identity only stands for a `.strm` file: a native
// storage path bound without its storage location never gets a grant.
func TestNativeVideoWorkerGrantRefusesLocationFreeStorageIdentities(t *testing.T) {
	path := "bloem-storage:" + uuid.NewString()
	s, session, start := localStrmGrantFixture(t, path)
	store := NewNativeVideoWorkerGrantStore(nativeVideoGrantRedis(t))
	if err := store.Prepare(t.Context(), 7, s, session, start); err == nil {
		t.Fatal("Prepare admitted a storage path without its storage location")
	}
}
