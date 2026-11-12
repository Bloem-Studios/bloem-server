package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remotesource"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

func strmWorkerRedis(t *testing.T) *redis.Client {
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

type strmCatalog struct{ file *models.MediaFile }

func (c strmCatalog) GetByID(_ context.Context, id int) (*models.MediaFile, error) {
	if c.file == nil || c.file.ID != id {
		return nil, nil
	}
	current := *c.file
	return &current, nil
}

// strmHost is one process's composition of `.strm` playback: its own guard
// (and so its own private-address setting) and the shared Redis control plane.
// The API server and the worker each build one.
func strmHost(client *redis.Client, file *models.MediaFile, sources strmSources, allowPrivate *atomic.Bool, storage nativeStorageOpen, session *playback.Session, dials *atomic.Int32) *handlers.NativeVideoService {
	guard := remotesource.NewGuard(remotesource.Options{AllowPrivate: func(context.Context) bool { dials.Add(1); return allowPrivate.Load() }})
	strm := &strmPlayback{sources: sources, guard: guard}
	authority := nativestorage.PlaybackAuthority{AuthorizeFile: func(_ context.Context, requested *models.MediaFile) (*models.MediaFile, error) {
		current := *file
		return &current, nil
	}}
	service := &handlers.NativeVideoService{Files: strmCatalog{file: file}, Bindings: handlers.NewNativeVideoBindingStore(client), WorkerGrants: handlers.NewNativeVideoWorkerGrantStore(client), Deny: playback.NewStreamDeny(client)}
	service.Open = func(ctx context.Context, requested *models.MediaFile) (handlers.NativeVideoFile, error) {
		return strm.open(ctx, requested, authority, storage)
	}
	service.OpenDelegated = func(ctx context.Context, requested *models.MediaFile, binding handlers.NativeVideoBinding) (handlers.NativeVideoFile, error) {
		opened, err := strm.open(ctx, requested, authority, storage)
		if err != nil {
			return nil, err
		}
		if opened.Identity() != binding.Identity {
			_ = opened.Close()
			return nil, storagesource.ErrReferenceConflict
		}
		return opened, nil
	}
	service.Delegation = func(context.Context) (handlers.NativeVideoAuthority, error) {
		return handlers.NativeVideoAuthority{UserID: session.UserID, ProfileID: session.ProfileID, TokenType: "stream", PlaybackSessionID: session.ID, ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
	}
	return service
}

func strmSyntheticMKV(t *testing.T, ffmpeg string) []byte {
	t.Helper()
	media := filepath.Join(t.TempDir(), "remote.mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=128x96:r=24", "-t", "3", "-c:v", "libx264", "-threads", "1", "-g", "24", "-pix_fmt", "yuv420p", media).CombinedOutput(); err != nil {
		t.Fatalf("synthetic media: %v %s", err, out)
	}
	data, err := os.ReadFile(media)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// strmWorker is a real transcode worker behind Bloem's native execution
// wrapper, resolving grants with its own host composition.
func strmWorker(t *testing.T, ffmpeg string, nodeID int, worker *handlers.NativeVideoService) http.Handler {
	t.Helper()
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "synthetic-worker-secret"
	cfg.Playback.TranscodeDir, cfg.Playback.FFmpegPath, cfg.Playback.HWAccel = t.TempDir(), ffmpeg, "none"
	watcher := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
	watcher.SetConfigForTest(cfg)
	server := transcodenode.NewServer(watcher, nodesessions.NewTracker(nil, "strm-node", "strm-node", "transcode"))
	server.SetInputPathAuthorizer(transcodenode.NewNativeInputPathAuthorizer(nil))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return server.WrapNativeExecution(server.Handler(), transcodenode.NativeExecutionConfig{NodeID: nodeID, Resolve: func(ctx context.Context, request transcodenode.NativeExecutionRequest) (*transcodenode.NativeExecutionGrant, error) {
		return worker.WorkerGrants.Resolve(ctx, request, worker)
	}})
}

func strmWorkerStart(t *testing.T, handler http.Handler, start transcodenode.TranscodeStartRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/transcode/start", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer synthetic-worker-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// strmControlPlane returns the worker grant and binding records in Redis for
// a session, so a test can check that none of them holds the URL.
func strmControlPlane(t *testing.T, client *redis.Client, session string, transport string, fileID int) string {
	t.Helper()
	var all strings.Builder
	for _, key := range []string{"bloem:native-video-worker:" + transport, "bloem:native-video:" + session + ":" + strconv.Itoa(fileID)} {
		value, err := client.Get(t.Context(), key).Result()
		if err != nil {
			t.Fatalf("control-plane record %s: %v", key, err)
		}
		all.WriteString(value)
	}
	return all.String()
}

// storedStrmObject is a storage object whose listing matches its reference,
// as the coordinator's opened objects do.
type storedStrmObject struct{ *fakeStoredStrm }

func (f *storedStrmObject) Info() mediasource.Info {
	ref := f.identity.Reference
	return mediasource.Info{Name: path.Base(ref.LogicalPath), LogicalPath: ref.LogicalPath, Revision: ref.Revision, Size: 60}
}

// End to end: the API server binds a `.strm` session and seals a worker grant;
// a separate worker host resolves that grant, opens the URL through its own
// guard and runs a real ffmpeg HLS encode. Neither the start request nor any
// control-plane record holds the URL. Local and storage `.strm` items both go
// this way.
func TestStrmWorkerHLSRunsOnTheWorkerThroughItsOwnGuard(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required")
	}
	client := strmWorkerRedis(t)
	data := strmSyntheticMKV(t, ffmpeg)
	remoteReads := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteReads.Add(1)
		http.ServeContent(w, r, "remote", time.Unix(1_700_000_000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	remoteURL := server.URL + "/films/harbor.mkv?token=very-secret"

	location := uuid.New()
	storedPath, _ := storagesource.CatalogLocation(location, "movies/harbor.strm")
	for _, tc := range []struct {
		name           string
		path           string
		sourceRevision string
		storage        func(*models.MediaFile) nativeStorageOpen
	}{
		{name: "local", path: "/media/movies/Harbor Lights (2019)/Harbor Lights (2019).strm", storage: func(*models.MediaFile) nativeStorageOpen {
			return func(context.Context, *models.MediaFile, nativestorage.PlaybackAuthority) (handlers.NativeVideoFile, error) {
				t.Error("a local .strm opened through a storage source")
				return nil, storagesource.ErrSourceUnavailable
			}
		}},
		{name: "storage", path: storedPath, sourceRevision: "object-version-3", storage: func(file *models.MediaFile) nativeStorageOpen {
			return func(ctx context.Context, _ *models.MediaFile, _ nativestorage.PlaybackAuthority) (handlers.NativeVideoFile, error) {
				lifetime, cancel := context.WithCancel(ctx)
				return &storedStrmObject{fakeStoredStrm: &fakeStoredStrm{identity: nativestorage.PlaybackIdentity{FileID: file.ID, MediaFolderID: file.MediaFolderID, ContentID: file.ContentID, FilePath: file.FilePath,
					Reference: storagesource.PersistedRef{LocationID: location, EntryID: "movies/harbor.strm", Revision: "object-version-3", LogicalPath: "movies/harbor.strm"}}, ctx: lifetime, cancel: cancel}}, nil
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runStrmWorkerCase(t, ffmpeg, client, server, remoteURL, remoteReads, tc.name, tc.path, tc.sourceRevision, tc.storage)
		})
	}
}

// runStrmWorkerCase is one item of TestStrmWorkerHLSRunsOnTheWorkerThroughItsOwnGuard.
func runStrmWorkerCase(t *testing.T, ffmpeg string, client *redis.Client, server *httptest.Server, remoteURL string, remoteReads *atomic.Int32, name, filePath, sourceRevision string, storageFor func(*models.MediaFile) nativeStorageOpen) {
	t.Helper()
	tc := struct {
		name, path, sourceRevision string
		storage                    func(*models.MediaFile) nativeStorageOpen
	}{name: name, path: filePath, sourceRevision: sourceRevision, storage: storageFor}
	file := &models.MediaFile{ID: 4100 + len(tc.name), MediaFolderID: 7, ContentID: "movie-harbor-" + tc.name, FilePath: tc.path, Container: "mkv", CodecVideo: "h264", VideoTracks: []models.VideoTrack{{Codec: "h264"}}, Duration: 3}
	session := &playback.Session{ID: uuid.NewString(), UserID: 3, ProfileID: "p", MediaFileID: file.ID, PlayMethod: playback.PlayTranscode, StartedAt: time.Now().UTC()}
	sources := &fakeStrmSources{rows: map[string]remotesource.Source{}}
	sources.put(file.FilePath, remoteURL, tc.sourceRevision)
	var apiAllow, workerAllow atomic.Bool
	apiAllow.Store(true)
	workerAllow.Store(true)
	var apiDials, workerDials atomic.Int32
	api := strmHost(client, file, sources, &apiAllow, tc.storage(file), session, &apiDials)
	worker := strmHost(client, file, sources, &workerAllow, tc.storage(file), session, &workerDials)
	transport := uuid.NewString()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = api.WorkerGrants.RevokeTransport(ctx, transport)
		_ = client.Del(ctx, "bloem:native-video:"+session.ID+":"+strconv.Itoa(file.ID), "bloem:native-video-session:"+session.ID, playback.StreamDenyKey(session.ID)).Err()
	})
	if err := api.Bind(t.Context(), session, file); err != nil {
		t.Fatalf("API Bind = %v", err)
	}
	start := transcodenode.TranscodeStartRequest{SessionID: transport, InputPath: file.FilePath, SourceVideoCodec: "h264", TargetCodecVideo: "h264", TargetCodecAudio: "copy", TargetResolution: "720p",
		TargetBitrateKbps: 500, SegmentDuration: 1, AudioTrackIndex: -1, SubtitleTrackIndex: -1, TotalDuration: 3, HWAccel: "none", RequireReady: true}
	if err := api.WorkerGrants.Prepare(t.Context(), 7, api, session, start); err != nil {
		t.Fatalf("API Prepare = %v", err)
	}
	body, _ := json.Marshal(start)
	records := strmControlPlane(t, client, session.ID, transport, file.ID)
	for _, leak := range []string{"very-secret", "harbor.mkv", server.Listener.Addr().String()} {
		if strings.Contains(string(body), leak) || strings.Contains(records, leak) {
			t.Fatalf("the start or a control-plane record names the URL (%q)", leak)
		}
	}

	// A worker whose own guard refuses the address gets no grant: it
	// checks the URL itself rather than trusting the API's check.
	workerAllow.Store(false)
	refused := strmWorkerStart(t, strmWorker(t, ffmpeg, 7, worker), start)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("worker start with its guard refusing = %d %s", refused.Code, refused.Body.String())
	}
	workerAllow.Store(true)

	before, beforeDials := remoteReads.Load(), workerDials.Load()
	handler := strmWorker(t, ffmpeg, 7, worker)
	response := strmWorkerStart(t, handler, start)
	if response.Code != http.StatusAccepted {
		t.Fatalf("worker start = %d %s", response.Code, response.Body.String())
	}
	if remoteReads.Load() == before || workerDials.Load() == beforeDials {
		t.Fatal("the worker did not read the remote media through its own guard")
	}
	// The API accepts the attested recipe; the worker's playlist reads
	// then carry its stream token, which names the catalog path only.
	card := playback.NewRecipeCard(session.UserID, session.ProfileID, file.ID, "", playback.TranscodeOpts{SessionID: session.ID, TranscodeTransportID: transport, InputPath: file.FilePath,
		SourceVideoCodec: "h264", TargetCodecVideo: "h264", TargetCodecAudio: "copy", TargetResolution: "720p", TargetBitrateKbps: 500, SegmentDuration: 1, AudioTrackIndex: -1, SubtitleTrackIndex: -1, TotalDuration: 3, HWAccel: "none"})
	card.RoutingExecutionNodeID = 7
	if err := api.WorkerGrants.Accept(t.Context(), 7, transport, card); err != nil {
		t.Fatalf("API Accept = %v", err)
	}
	token, err := streamtoken.Sign(card.ToClaims(), "synthetic-worker-secret", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claims, _ := json.Marshal(card.ToClaims()); strings.Contains(string(claims), "very-secret") || strings.Contains(string(claims), "harbor.mkv") {
		t.Fatal("the stream token names the URL")
	}
	// The start waited for the first playlist (RequireReady), so it is
	// there now; the read carries the accepted recipe's token.
	request := httptest.NewRequest(http.MethodGet, "/transcode/"+transport+"/master.m3u8", nil)
	request.Header.Set("Authorization", "Bearer synthetic-worker-secret")
	request.Header.Set("X-Silo-Stream-Token", token)
	playlist := httptest.NewRecorder()
	handler.ServeHTTP(playlist, request)
	if playlist.Code != http.StatusOK || !bytes.Contains(playlist.Body.Bytes(), []byte("#EXTM3U")) {
		t.Fatalf("worker playlist = %d %s", playlist.Code, playlist.Body.String())
	}

	// A different worker never resolves this grant.
	if other := strmWorkerStart(t, strmWorker(t, ffmpeg, 8, worker), start); other.Code != http.StatusForbidden {
		t.Fatalf("another worker's start = %d", other.Code)
	}
}
