package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediaprobe"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tonemap"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func nativeVideoGrantRedis(t *testing.T) *redis.Client {
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

func nativeVideoGrantFixture(t *testing.T, client *redis.Client) (*NativeVideoService, *playback.Session, transcodenode.TranscodeStartRequest, playback.RecipeCard) {
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
		_ = NewNativeVideoWorkerGrantStore(client).Revoke(ctx, 7, transport)
		key, _ := nativeVideoBindingKey(session.ID, file.ID)
		_ = client.Del(ctx, key, playback.StreamDenyKey(session.ID)).Err()
	})
	return s, session, start, card
}

// Losing the exact Start digest would let a caller choose a different encode
// while retaining a host source grant for the original plan.
func TestNativeVideoWorkerStartRequiresExactPreparedRecipe(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	s, session, start, _ := nativeVideoGrantFixture(t, client)
	store := NewNativeVideoWorkerGrantStore(client)
	if err := store.Prepare(t.Context(), 7, s, session, start); err != nil {
		t.Fatal(err)
	}
	request := transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Start: &start}
	grant, err := store.Resolve(t.Context(), request, s)
	if err != nil || grant == nil || grant.PlaybackSessionID != session.ID || grant.TransportID != start.SessionID {
		t.Fatalf("prepared start rejected: %v", err)
	}
	if _, err := store.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath}, s); err == nil {
		t.Fatal("unaccepted grant admitted a GET")
	}
	for _, change := range []func(*transcodenode.NativeExecutionRequest){
		func(r *transcodenode.NativeExecutionRequest) { r.WorkerID = 8 },
		func(r *transcodenode.NativeExecutionRequest) { r.TransportID = uuid.NewString() },
		func(r *transcodenode.NativeExecutionRequest) { r.InputPath += "other" },
		func(r *transcodenode.NativeExecutionRequest) { r.Start.TargetBitrateKbps = 1000 },
		func(r *transcodenode.NativeExecutionRequest) { r.Start.AudioTrackIndex = 2 },
		func(r *transcodenode.NativeExecutionRequest) { r.Start.VideoBitstreamFilter = "extract_extradata" },
	} {
		candidate, body := request, start
		candidate.Start = &body
		change(&candidate)
		if _, err := store.Resolve(t.Context(), candidate, s); err == nil {
			t.Fatal("changed worker/start recipe received a source grant")
		}
	}
}

// Recovery uses the server's accepted portable recipe; neither a valid source
// binding nor a signed card for a different encode can replace that recipe.
func TestNativeVideoWorkerRecoveryRequiresAcceptedRecipe(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	s, session, start, card := nativeVideoGrantFixture(t, client)
	store := NewNativeVideoWorkerGrantStore(client)
	if err := store.Prepare(t.Context(), 7, s, session, start); err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(t.Context(), 7, start.SessionID, card); err != nil {
		t.Fatal(err)
	}
	claims := card.ToClaims()
	portable := playback.RecipeCardFromClaims(&claims)
	request := transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &portable}
	// A fresh store models a new worker process without local grant state.
	grant, err := NewNativeVideoWorkerGrantStore(client).Resolve(t.Context(), request, s)
	if err != nil || grant == nil {
		t.Fatalf("durable portable recipe rejected: %v", err)
	}
	for _, change := range []func(*playback.RecipeCard){
		func(c *playback.RecipeCard) { c.TargetBitrateKbps = 1000 },
		func(c *playback.RecipeCard) { c.AudioTrackIndex = 2 },
		func(c *playback.RecipeCard) { c.UserID = 2 },
		func(c *playback.RecipeCard) { c.MediaFileID = 43 },
		func(c *playback.RecipeCard) { c.OutputSubdir = "other" },
		func(c *playback.RecipeCard) { c.RoutingExecutionNodeID = 8 },
	} {
		candidate := portable
		change(&candidate)
		request.Recipe = &candidate
		if _, err := store.Resolve(t.Context(), request, s); err == nil {
			t.Fatal("changed reconstruction recipe received a source grant")
		}
	}
}

// A tool input already opened under an accepted grant must fail and join
// after revocation, even when an earlier range succeeded.
func TestNativeVideoWorkerRevocationStopsOpenedInput(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	s, session, start, card := nativeVideoGrantFixture(t, client)
	store := NewNativeVideoWorkerGrantStore(client)
	if err := store.Prepare(t.Context(), 7, s, session, start); err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(t.Context(), 7, start.SessionID, card); err != nil {
		t.Fatal(err)
	}
	grant, err := store.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}, s)
	if err != nil {
		t.Fatal(err)
	}
	input, err := grant.InputSource.Open(t.Context(), playback.TranscodeOpts{SessionID: start.SessionID, InputPath: start.InputPath})
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	err = input.Run(func(ctx context.Context, argument string) error {
		transport := &http.Transport{Proxy: nil}
		defer transport.CloseIdleConnections()
		httpClient := &http.Client{Transport: transport}
		read := func() (int, string, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, argument, nil)
			if err != nil {
				return 0, "", err
			}
			req.Header.Set("Range", "bytes=0-3")
			resp, err := httpClient.Do(req)
			if err != nil {
				return 0, "", err
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			return resp.StatusCode, string(data), err
		}
		status, data, err := read()
		if err != nil || status != http.StatusPartialContent || data != "0123" {
			return errors.New("first pinned range failed")
		}
		if err := store.Revoke(ctx, 7, start.SessionID); err != nil {
			return err
		}
		status, data, err = read()
		if err == nil && (status == http.StatusPartialContent || data == "0123") {
			return errors.New("revoked pinned range was exposed")
		}
		return nil
	})
	if err == nil {
		t.Fatal("revoked input reported successful completion")
	}
}

func TestNativeVideoWorkerGrantsRequireRedis(t *testing.T) {
	if err := NewNativeVideoWorkerGrantStore(nil).Prepare(t.Context(), 7, nil, nil, transcodenode.TranscodeStartRequest{}); err == nil {
		t.Fatal("Redis-less worker issued a grant")
	}
}

func TestNativeVideoWorkerBindingAndDenyRechecked(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	for _, change := range []string{"actor", "logical-path", "stat-size", "generation", "artifact", "configuration", "deny"} {
		t.Run(change, func(t *testing.T) {
			s, session, start, card := nativeVideoGrantFixture(t, client)
			store := NewNativeVideoWorkerGrantStore(client)
			if err := store.Prepare(t.Context(), 7, s, session, start); err != nil {
				t.Fatal(err)
			}
			if err := store.Accept(t.Context(), 7, start.SessionID, card); err != nil {
				t.Fatal(err)
			}
			request := transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}
			grant, err := store.Resolve(t.Context(), request, s)
			if err != nil {
				t.Fatal(err)
			}
			if change == "deny" {
				if err := client.Set(t.Context(), playback.StreamDenyKey(session.ID), "1", time.Minute).Err(); err != nil {
					t.Fatal(err)
				}
			} else {
				binding, err := s.Bindings.Get(t.Context(), session.ID, session.MediaFileID)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "actor":
					binding.Authority.APIKeyRevision++
				case "logical-path":
					binding.Info.LogicalPath += "changed"
				case "stat-size":
					binding.Info.Size++
				case "generation":
					binding.Identity.Generation++
				case "artifact":
					binding.Identity.ArtifactChecksum = strings.Repeat("b", 64)
				case "configuration":
					binding.Identity.ConfigurationRevision++
				}
				key, _ := nativeVideoBindingKey(session.ID, session.MediaFileID)
				data, err := json.Marshal(binding)
				if err != nil {
					t.Fatal(err)
				}
				if err := client.Set(t.Context(), key, data, time.Hour).Err(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Resolve(t.Context(), request, s); err == nil {
				t.Fatal("changed authority admitted a new source")
			}
			if input, err := grant.InputSource.Open(t.Context(), playback.TranscodeOpts{SessionID: start.SessionID, InputPath: start.InputPath}); err == nil {
				_ = input.Close()
				t.Fatal("captured source factory ignored changed authority")
			}
		})
	}
}

func TestNativeVideoWorkerValidationRetainsCatalogRevision(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	s, session, start, card := nativeVideoGrantFixture(t, client)
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "validation.mp4")
	command := exec.CommandContext(t.Context(), bin, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=24", "-t", "1", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("video fixture: %v %s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := s.Files.GetByID(t.Context(), session.MediaFileID)
	if err != nil {
		t.Fatal(err)
	}
	track, err := mediaprobe.ProbePrimaryVideoTrack(t.Context(), mediaprobe.FFprobePathFromFFmpeg(bin), path)
	if err != nil {
		t.Fatal(err)
	}
	file.FileSize, file.VideoTracks = int64(len(data)), []models.VideoTrack{track}
	binding, err := s.Bindings.Get(t.Context(), session.ID, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding.Info.Size = int64(len(data))
	s.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		return &videoFileFixture{Reader: bytes.NewReader(data), id: binding.Identity, info: binding.Info, closed: new(int)}, nil
	}
	payload, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := nativeVideoBindingKey(session.ID, file.ID)
	if err := client.Set(t.Context(), key, payload, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	store := NewNativeVideoWorkerGrantStore(client)
	if err := store.Prepare(t.Context(), 7, s, session, start); err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(t.Context(), 7, start.SessionID, card); err != nil {
		t.Fatal(err)
	}
	grant, err := store.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}, s)
	if err != nil {
		t.Fatal(err)
	}
	if grant.InputSource.Validate == nil {
		t.Fatal("worker adapter dropped catalog validation")
	}
	opts := playback.TranscodeOpts{SessionID: start.SessionID, InputPath: start.InputPath, InputSource: grant.InputSource, InputFormat: grant.InputFormat, FFmpegPath: bin, ToneMapSourceRevision: tonemap.RevisionForFile(file)}
	if err := grant.InputSource.Validate(t.Context(), opts); err != nil {
		t.Fatalf("unchanged catalog rejected: %v", err)
	}
	file.FileSize++
	if err := grant.InputSource.Validate(t.Context(), opts); !errors.Is(err, tonemap.ErrSourceRevisionChanged) {
		t.Fatalf("stale catalog revision admitted: %v", err)
	}
}

func TestNativeVideoWorkerProgressiveGrantAndImmutableAcceptance(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	s, session, start, card := nativeVideoGrantFixture(t, client)
	store := NewNativeVideoWorkerGrantStore(client)
	card.PlayMethod = playback.PlayRemux
	if err := store.PrepareRecipe(t.Context(), 7, s, session, card); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRecipe(t.Context(), 7, s, session, card); err != nil {
		t.Fatalf("idempotent prepare failed: %v", err)
	}
	request := transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}
	if _, err := store.Resolve(t.Context(), request, s); err != nil {
		t.Fatal(err)
	}
	altered := card
	altered.TargetBitrateKbps++
	if err := store.Accept(t.Context(), 7, start.SessionID, altered); err == nil {
		t.Fatal("accepted recipe replaced")
	}
	if err := store.PrepareRecipe(t.Context(), 8, s, session, card); err == nil {
		t.Fatal("transport reassigned to another worker")
	}
	if err := store.Revoke(t.Context(), 8, start.SessionID); err == nil {
		t.Fatal("different worker revoked grant")
	}
	if _, err := store.Resolve(t.Context(), request, s); err != nil {
		t.Fatalf("wrong worker removed valid grant: %v", err)
	}
	if err := store.RevokeTransport(t.Context(), start.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(t.Context(), request, s); err == nil {
		t.Fatal("stop hook left source grant alive")
	}
}

func TestNativeVideoWorkerGrantExpiryCancelsInput(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	s, session, start, card := nativeVideoGrantFixture(t, client)
	binding, err := s.Bindings.Get(t.Context(), session.ID, session.MediaFileID)
	if err != nil {
		t.Fatal(err)
	}
	binding.Authority.ExpiresAt = time.Now().UTC().Add(300 * time.Millisecond)
	data, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := nativeVideoBindingKey(session.ID, session.MediaFileID)
	if err := client.Set(t.Context(), key, data, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	store := NewNativeVideoWorkerGrantStore(client)
	if err := store.Prepare(t.Context(), 7, s, session, start); err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(t.Context(), 7, start.SessionID, card); err != nil {
		t.Fatal(err)
	}
	grant, err := store.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}, s)
	if err != nil {
		t.Fatal(err)
	}
	if !grant.ExpiresAt.Equal(binding.Authority.ExpiresAt) {
		t.Fatal("grant extended original actor authority")
	}
	input, err := grant.InputSource.Open(t.Context(), playback.TranscodeOpts{SessionID: start.SessionID, InputPath: start.InputPath})
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	err = input.Run(func(ctx context.Context, _ string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			return errors.New("expiry did not cancel source")
		}
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expiry result: %v", err)
	}
}

func TestNativeVideoWorkerGeneratedTransport(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, start, card := nativeVideoGrantFixture(t, client)
	start.SessionID = transportGenerationV3(session.ID, "plan:abcd1234abcd5678")
	card.TranscodeTransportID = start.SessionID
	store := NewNativeVideoWorkerGrantStore(client)
	t.Cleanup(func() { _ = store.RevokeTransport(context.Background(), start.SessionID) })
	if err := store.Prepare(t.Context(), 7, service, session, start); err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(t.Context(), 7, start.SessionID, card); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Recipe: &card}, service); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"not-a-host-transport", start.SessionID + "/", start.SessionID + ":", start.SessionID + strings.Repeat("x", 128)} {
		if _, err := nativeVideoWorkerKey(7, bad); err == nil {
			t.Fatal("invalid transport admitted")
		}
	}
}

func TestNativeVideoWorkerConcurrentAcceptCannotUndoRevoke(t *testing.T) {
	client := nativeVideoGrantRedis(t)
	service, session, start, card := nativeVideoGrantFixture(t, client)
	store := NewNativeVideoWorkerGrantStore(client)
	for attempt := 0; attempt < 40; attempt++ {
		if err := store.Prepare(t.Context(), 7, service, session, start); err != nil {
			t.Fatal(err)
		}
		ready := make(chan struct{})
		joined := make(chan struct{})
		go func() { defer close(joined); <-ready; _ = store.Accept(t.Context(), 7, start.SessionID, card) }()
		close(ready)
		if err := store.Revoke(t.Context(), 7, start.SessionID); err != nil {
			t.Fatal(err)
		}
		<-joined
		key, _ := nativeVideoWorkerKey(7, start.SessionID)
		if exists, err := client.Exists(t.Context(), key).Result(); err != nil || exists != 0 {
			t.Fatalf("concurrent acceptance retained revoked source: %d %v", exists, err)
		}
	}
}
