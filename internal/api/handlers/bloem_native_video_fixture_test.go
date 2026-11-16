package handlers

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// nativeVideoRedis is the test Redis that native bindings and deny markers
// are stored in.
func nativeVideoRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("SILO_TEST_REDIS_URL")
	if url == "" {
		t.Skip("SILO_TEST_REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal("invalid test Redis configuration")
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatal("test Redis is unavailable")
	}
	return client
}

// nativeVideoBoundFixture is a stored movie bound to a playback session,
// with the start request and recipe card a transcode of it would carry.
func nativeVideoBoundFixture(t *testing.T, client *redis.Client) (*NativeVideoService, *playback.Session, transcodenode.TranscodeStartRequest, playback.RecipeCard) {
	t.Helper()
	s, file, session, identity, closed := nativeVideoFixture(t)
	identity.SourceKey, identity.OwnerID, identity.Reference.LocationID = uuid.New(), uuid.New(), uuid.New()
	identity.InstallationID, identity.PluginID, identity.ProviderSourceID, identity.RootEntryID = 3, "fixture", "source", "root"
	identity.ConfigurationRevision, identity.Generation, identity.ArtifactChecksum = 1, 1, strings.Repeat("a", 64)
	identity.Reference.EntryID, identity.Reference.LogicalPath = "movie", "Movies/movie.mp4"
	file.FilePath, _ = storagesource.CatalogLocation(identity.Reference.LocationID, identity.Reference.EntryID)
	identity.FilePath = file.FilePath
	s.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		return &videoFileFixture{Reader: bytes.NewReader([]byte("0123456789")), id: *identity, info: mediasource.Info{Name: "movie.mp4", LogicalPath: identity.Reference.LogicalPath, Size: 10, Revision: identity.Reference.Revision}, closed: closed}, nil
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
	start := transcodenode.TranscodeStartRequest{SessionID: transport, InputPath: file.FilePath, SourceVideoCodec: "h264", SourceAudioChannels: 2,
		TargetCodecVideo: "h264", TargetCodecAudio: "copy", TargetResolution: "720p", TargetBitrateKbps: 2000, SegmentDuration: 6,
		AudioTrackIndex: -1, SubtitleTrackIndex: -1, TotalDuration: 10, HWAccel: "none", RequireReady: true}
	card := playback.NewRecipeCard(session.UserID, session.ProfileID, file.ID, "", playback.TranscodeOpts{
		SessionID: session.ID, TranscodeTransportID: transport, InputPath: file.FilePath, SourceVideoCodec: "h264", SourceAudioChannels: 2,
		TargetCodecVideo: "h264", TargetCodecAudio: "copy", TargetResolution: "720p", TargetBitrateKbps: 2000, SegmentDuration: 6,
		AudioTrackIndex: -1, SubtitleTrackIndex: -1, TotalDuration: 10, HWAccel: "none", FastStart: true})
	card.RoutingExecutionNodeID = 7
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		key, _ := nativeVideoBindingKey(session.ID, file.ID)
		_ = client.Del(ctx, key, playback.StreamDenyKey(session.ID)).Err()
	})
	return s, session, start, card
}

// nativeDeliverySource counts a source's reads and closes and fails its
// authority checks once revoked is set.
type nativeDeliverySource struct {
	NativeVideoFile
	revoked *atomic.Bool
	reads   *atomic.Int32
	closes  *atomic.Int32
}

func (f *nativeDeliverySource) Reauthorize(ctx context.Context) error {
	if f.revoked.Load() {
		return errors.New("delivery source unavailable")
	}
	return f.NativeVideoFile.Reauthorize(ctx)
}
func (f *nativeDeliverySource) Read(p []byte) (int, error) {
	f.reads.Add(1)
	return f.NativeVideoFile.Read(p)
}
func (f *nativeDeliverySource) ReadAt(p []byte, offset int64) (int, error) {
	f.reads.Add(1)
	return f.NativeVideoFile.ReadAt(p, offset)
}
func (f *nativeDeliverySource) Close() error {
	f.closes.Add(1)
	return f.NativeVideoFile.Close()
}
