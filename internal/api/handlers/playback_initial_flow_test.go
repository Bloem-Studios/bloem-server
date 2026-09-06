package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/playback/planstore"
	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

type initialHTTPFixture struct {
	manager *playback.SessionManager
	handler *PlaybackHandler
	file    *models.MediaFile
	flow    *InitialPlaybackFlowV3
	server  *httptest.Server
	pool    *pgxpool.Pool
	request playback.StartRequestV3
	source  userstore.PlaybackSinkHandle
	userID  int
	itemID  string
}

func newInitialHTTPFixture(t *testing.T) *initialHTTPFixture {
	return newInitialHTTPSourceFixture(t, "postgres")
}

func newInitialHTTPSourceFixture(t *testing.T, backend string) *initialHTTPFixture {
	t.Helper()
	dsn, redisURL := os.Getenv("SILO_TEST_DATABASE_URL"), os.Getenv("SILO_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("actual initial HTTP flow requires SILO_TEST_DATABASE_URL and SILO_TEST_REDIS_URL")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	f := &initialHTTPFixture{pool: pool, request: v3HandlerStartRequest()}
	var folderID int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username) VALUES($1) RETURNING id`, "initial-http-"+uuid.NewString()).Scan(&f.userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(c, `DELETE FROM users WHERE id=$1`, f.userID)
		_, _ = pool.Exec(c, `DELETE FROM media_folders WHERE id=$1`, folderID)
	})
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies',$1) RETURNING id`, uuid.NewString()).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	file := v3HandlerFixtureFile(t)
	f.file = file
	file.Duration = 1000
	if err := os.WriteFile(file.FilePath, bytes.Repeat([]byte("initial-flow-media"), 8192), 0600); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_files(media_folder_id,file_path) VALUES($1,$2) RETURNING id`, folderID, file.FilePath).Scan(&file.ID); err != nil {
		t.Fatal(err)
	}
	file.ContentID = "initial-http-item-" + uuid.NewString()
	f.itemID = file.ContentID
	f.request.FileID = file.ID
	f.request.ProfileID = uuid.NewString()
	f.request.PlaybackAttemptID = uuid.NewString()
	var provider userstore.PlaybackSourceProvider = pgstore.NewPostgresProvider(pool)
	pgProvider := pgstore.NewPostgresProvider(pool)
	store, err := pgProvider.ForUser(ctx, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProfile(ctx, userstore.Profile{ID: f.request.ProfileID, Name: "Initial HTTP"}); err != nil {
		t.Fatal(err)
	}
	ref := userstore.PlaybackSourceRef{Backend: backend, AccountID: f.userID, SourceID: uuid.NewString(), SelectionGeneration: 1}
	if backend == "sqlite" {
		dir := t.TempDir()
		db, err := userdb.NewUserDB(filepath.Join(dir, fmt.Sprintf("%d.db", f.userID)), f.userID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB.Exec(`INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES(?,?,1,'writable')`, f.userID, ref.SourceID); err != nil {
			t.Fatal(err)
		}
		if err := userdb.NewSQLiteUserStore(db.DB).CreateProfile(ctx, userstore.Profile{ID: f.request.ProfileID, Name: "Initial HTTP"}); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		sqlite := userdb.NewSQLiteProvider(userdb.NewUserDBPool(userdb.PoolConfig{DataDir: dir}))
		t.Cleanup(func() { _ = sqlite.Close() })
		provider = sqlite
	} else {
		if _, err := pool.Exec(ctx, `INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES($1,$2,1,'writable')`, f.userID, ref.SourceID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO playback_source_registrations(user_id,backend,source_id,selection_generation,admission_id,admission_state) VALUES($1,$4,$2,1,$3,'admitting')`, f.userID, ref.SourceID, uuid.NewString(), backend); err != nil {
		t.Fatal(err)
	}
	f.source, err = provider.OpenPlaybackSink(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.source.Close() })
	control, err := planstore.NewPostgresWithGrantPolicy(pool, playback.AttemptGrantPolicyV3{MaxDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		t.Fatal(err)
	}
	policy := playback.RuntimeGrantPolicyV3{MaxDuration: time.Second, SafetyMargin: 100 * time.Millisecond, RenewBefore: 200 * time.Millisecond, PollInterval: time.Millisecond}
	recipes := noderecipe.NewStore(client, time.Minute)
	runtime, err := planstore.NewExecutorRuntime(control, recipes, 0, clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	manager := playback.NewSessionManager(0, 0)
	f.manager = manager
	handler := NewPlaybackHandler(manager, testPlaybackFileResolver{file: file})
	f.handler = handler
	handler.JWTSecret = "initial-http-fixture-secret"
	handler.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{"allow_4k_transcode": "true"}}
	handler.ItemAccess = allowAllPlaybackItemAccess{}
	f.flow = &InitialPlaybackFlowV3{Control: control, Sources: provider, Recipes: recipes, OwnerID: uuid.NewString(), Context: ctx, Clock: clock, Policy: policy, AcquireGrant: runtime.Acquire, ResolveRecipe: runtime.Resolve}
	if err := handler.ConfigureInitialPlaybackV3(f.flow); err != nil {
		t.Fatal(err)
	}
	stream := NewStreamHandler(manager, testPlaybackFileResolver{file: file})
	stream.JWTSecret = handler.JWTSecret
	stream.TM = handler.TranscodeManager()
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := apimw.SetClaims(r.Context(), &auth.Claims{UserID: f.userID, Role: "user", TokenType: auth.TokenTypeAccess})
			next.ServeHTTP(w, r.WithContext(apimw.SetProfileID(c, f.request.ProfileID)))
		})
	})
	router.Post("/start", handler.HandleStartPlayback)
	router.Get("/playback/transcode/{session_id}/master.m3u8", handler.HandleGetTranscodeManifest)
	router.Get("/playback/transcode/{session_id}/segment/{name}", handler.HandleGetTranscodeSegment)
	router.Get("/api/v1/playback/transcode/{session_id}/master.m3u8", handler.HandleGetTranscodeManifest)
	router.Get("/api/v1/playback/transcode/{session_id}/segment/{name}", handler.HandleGetTranscodeSegment)
	router.Post("/playback/{session_id}/progress", handler.HandleUpdateProgress)
	router.Delete("/playback/{session_id}", handler.HandleStopPlayback)
	router.Get("/stream/{session_id}", stream.HandleStream)
	router.Get("/api/v1/stream/{session_id}", stream.HandleStream)
	f.server = httptest.NewServer(router)
	t.Cleanup(f.server.Close)
	return f
}

func (f *initialHTTPFixture) call(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func TestInitialPlaybackHTTPDirectProgressStop(t *testing.T) {
	for _, backend := range []string{"postgres", "sqlite"} {
		t.Run(backend, func(t *testing.T) { testInitialHTTPDirectProgressStop(t, newInitialHTTPSourceFixture(t, backend)) })
	}
}

func testInitialHTTPDirectProgressStop(t *testing.T, f *initialHTTPFixture) {
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("start %d: %s", status, data)
	}
	var start playback.DecisionResponseV3
	if err := json.Unmarshal(data, &start); err != nil {
		t.Fatal(err)
	}
	if start.PlaybackPlan == nil {
		t.Fatalf("missing plan: %s", data)
	}
	sessionID := start.PlaybackPlan.SessionID
	status, replayed := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated || !bytes.Equal(data, replayed) {
		t.Fatalf("start replay: %d %s", status, replayed)
	}
	if len(f.manager.AllSessions()) != 1 {
		t.Fatalf("duplicate start created %d sessions", len(f.manager.AllSessions()))
	}

	streamURL := start.PlaybackPlan.Stream.URL
	if streamURL == "" {
		t.Fatal("missing direct URL")
	}
	if strings.HasPrefix(streamURL, "http") {
		t.Fatalf("unexpected external fixture URL: %s", streamURL)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+streamURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-31")
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusPartialContent || readErr != nil || len(body) != 32 {
		t.Fatalf("range: %d bytes=%d err=%v body=%s", response.StatusCode, len(body), readErr, body)
	}
	for _, sample := range []map[string]any{{"sequence": 41, "position": 600}, {"sequence": 42, "position": 120}, {"sequence": 42, "position": 120}, {"sequence": 41, "position": 800}} {
		status, data = f.call(t, http.MethodPost, "/playback/"+sessionID+"/progress", sample)
		if status != 200 {
			t.Fatalf("progress %d: %s", status, data)
		}

		expectedSequence, expectedPosition := int64(42), 120.0
		if sample["sequence"] == 41 && sample["position"] == 600 {
			expectedSequence, expectedPosition = 41, 600
		}
		assertInitialHTTPMutationDTO(t, data, "", expectedSequence, expectedPosition)
	}
	scope := userstore.PlaybackProgressScope{ProfileID: f.request.ProfileID, SessionID: sessionID, MediaItemID: f.itemID}
	state, err := f.source.ReadPlaybackProgress(t.Context(), scope)
	if err != nil || state.Last == nil || state.Last.Sample.Sequence != 42 || state.Last.Sample.PositionSeconds != 120 {
		t.Fatalf("accepted progress: %+v %v", state, err)
	}
	stopID := uuid.NewString()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, data = f.call(t, http.MethodDelete, "/playback/"+sessionID, map[string]any{"stop_id": stopID})
		if status == 200 {
			break
		}
		if status != 202 || time.Now().After(deadline) {
			t.Fatalf("stop %d: %s", status, data)
		}
	}
	firstTerminal, err := f.source.ReadPlaybackProgress(t.Context(), scope)
	if err != nil || firstTerminal.Stop == nil || firstTerminal.Stop.History == nil {
		t.Fatalf("missing committed history: %+v %v", firstTerminal, err)
	}

	status, data = f.call(t, http.MethodDelete, "/playback/"+sessionID, map[string]any{"stop_id": stopID})
	if status != 200 {
		t.Fatalf("stop replay %d: %s", status, data)
	}
	assertInitialHTTPMutationDTO(t, data, stopID, 42, 120)
	terminal, err := f.source.ReadPlaybackProgress(t.Context(), scope)
	if err != nil || terminal.Stop == nil || terminal.Stop.StopID != stopID || !reflect.DeepEqual(firstTerminal, terminal) {
		t.Fatalf("terminal: %+v %v", terminal, err)
	}
}

func TestInitialPlaybackHTTPUnavailableSourceDoesNotActivate(t *testing.T) {
	f := newInitialHTTPFixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_source_markers SET gate='sealed' WHERE user_id=$1`, f.userID); err != nil {
		t.Fatal(err)
	}
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("unavailable source start: %d %s", status, data)
	}
	if len(f.manager.AllSessions()) != 0 {
		t.Fatal("unavailable source activated runtime")
	}
	var active int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM playback_v3_attempts WHERE user_id=$1 AND control_state='active'`, f.userID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatal("unavailable source published authority")
	}
}

type initialLostInstallProvider struct {
	userstore.PlaybackSourceProvider
	lost atomic.Bool
}

func (p *initialLostInstallProvider) OpenPlaybackSink(ctx context.Context, ref userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error) {
	handle, err := p.PlaybackSourceProvider.OpenPlaybackSink(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &initialLostInstallHandle{PlaybackSinkHandle: handle, provider: p}, nil
}

type initialLostInstallHandle struct {
	userstore.PlaybackSinkHandle
	provider *initialLostInstallProvider
}

func (h *initialLostInstallHandle) InstallPlaybackAuthority(ctx context.Context, request userstore.InstallPlaybackAuthorityRequest) (userstore.PlaybackProgressResult, error) {
	result, err := h.PlaybackSinkHandle.InstallPlaybackAuthority(ctx, request)
	if err == nil && h.provider.lost.CompareAndSwap(false, true) {
		return userstore.PlaybackProgressResult{}, errors.New("fixture lost committed install reply")
	}
	return result, err
}
func TestInitialPlaybackHTTPLostInstallReply(t *testing.T) {
	f := newInitialHTTPFixture(t)
	provider := &initialLostInstallProvider{PlaybackSourceProvider: f.flow.Sources}
	f.flow.Sources = provider
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("recover install reply: %d %s", status, data)
	}
	if !provider.lost.Load() {
		t.Fatal("source install was not exercised")
	}
	var response playback.DecisionResponseV3
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.PlaybackPlan == nil {
		t.Fatal("missing plan")
	}
	state, err := f.source.ReadPlaybackProgress(t.Context(), userstore.PlaybackProgressScope{ProfileID: f.request.ProfileID, SessionID: response.PlaybackPlan.SessionID, MediaItemID: f.itemID})
	if err != nil || state.Last != nil || state.Stop != nil || state.Fence.AttemptID != f.request.PlaybackAttemptID {
		t.Fatalf("installed source: %+v %v", state, err)
	}
}

func TestInitialPlaybackHTTPExpiredOwnerCannotServeOrProgress(t *testing.T) {
	f := newInitialHTTPFixture(t)
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("start: %d %s", status, data)
	}
	var response playback.DecisionResponseV3
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.PlaybackPlan == nil {
		t.Fatal("missing plan")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '1 second' WHERE playback_attempt_id=$1`, f.request.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	status, data = f.call(t, http.MethodGet, response.PlaybackPlan.Stream.URL, nil)
	if status < 400 {
		t.Fatalf("expired owner served: %d %s", status, data)
	}
	status, data = f.call(t, http.MethodPost, "/playback/"+response.PlaybackPlan.SessionID+"/progress", map[string]any{"sequence": 1, "position": 500})
	if status < 400 {
		t.Fatalf("expired owner accepted progress: %d %s", status, data)
	}
	state, err := f.source.ReadPlaybackProgress(t.Context(), userstore.PlaybackProgressScope{ProfileID: f.request.ProfileID, SessionID: response.PlaybackPlan.SessionID, MediaItemID: f.itemID})
	if err != nil || state.Last != nil {
		t.Fatalf("expired owner changed source: %+v %v", state, err)
	}
}

type initialLostPublishControl struct {
	InitialPlaybackControlV3
	lost atomic.Bool
}

func (c *initialLostPublishControl) PublishInitialActivation(ctx context.Context, binding playback.InitialActivationBindingV3, record playback.AttemptRecordV3) (playback.InitialActivationV3, error) {
	state, err := c.InitialPlaybackControlV3.PublishInitialActivation(ctx, binding, record)
	if err == nil && c.lost.CompareAndSwap(false, true) {
		return playback.InitialActivationV3{}, errors.New("fixture lost committed publish reply")
	}
	return state, err
}
func TestInitialPlaybackHTTPLostPublishReply(t *testing.T) {
	f := newInitialHTTPFixture(t)
	control := &initialLostPublishControl{InitialPlaybackControlV3: f.flow.Control}
	f.flow.Control = control
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("recover publish reply: %d %s", status, data)
	}
	if !control.lost.Load() {
		t.Fatal("control publish was not exercised")
	}
	if len(f.manager.AllSessions()) != 1 {
		t.Fatalf("published session count: %d", len(f.manager.AllSessions()))
	}
	var response playback.DecisionResponseV3
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.PlaybackPlan == nil {
		t.Fatal("missing plan")
	}
	status, data = f.call(t, http.MethodGet, response.PlaybackPlan.Stream.URL, nil)
	if status != http.StatusOK {
		t.Fatalf("published stream: %d %s", status, data)
	}
}

type initialLostPublishReadControl struct {
	initialLostPublishControl
	readLost atomic.Bool
}

func (c *initialLostPublishReadControl) ReadInitialActivation(ctx context.Context, binding playback.InitialActivationBindingV3) (playback.InitialActivationV3, error) {
	if c.lost.Load() && c.readLost.CompareAndSwap(false, true) {
		return playback.InitialActivationV3{}, errors.New("fixture lost first publication read")
	}
	return c.InitialPlaybackControlV3.ReadInitialActivation(ctx, binding)
}
func TestInitialPlaybackHTTPLostPublishAndReadRetry(t *testing.T) {
	f := newInitialHTTPFixture(t)
	control := &initialLostPublishReadControl{initialLostPublishControl: initialLostPublishControl{InitialPlaybackControlV3: f.flow.Control}}
	f.flow.Control = control
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusServiceUnavailable || !control.readLost.Load() {
		t.Fatalf("uncertain publish: %d %s", status, data)
	}
	status, data = f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("retry publish: %d %s", status, data)
	}
	var response playback.DecisionResponseV3
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.PlaybackPlan == nil || len(f.manager.AllSessions()) != 1 {
		t.Fatalf("retry failed to expose exactly one runtime: %s", data)
	}
	status, data = f.call(t, http.MethodPost, "/playback/"+response.PlaybackPlan.SessionID+"/progress", map[string]any{"sequence": 1, "position": 120})
	if status != http.StatusOK {
		t.Fatalf("recovered progress: %d %s", status, data)
	}
}

func TestInitialPlaybackHTTPLocalTranscode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("actual local transcode requires ffmpeg")
	}
	f := newInitialHTTPFixture(t)
	command := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "12", "-c:v", "mpeg4", "-c:a", "aac", f.file.FilePath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("synthetic video: %v %s", err, output)
	}
	f.file.CodecVideo = "mpeg4"
	f.file.Duration = 12
	f.file.Resolution = "180p"
	f.file.VideoTracks = []models.VideoTrack{{Codec: "mpeg4", Width: 320, Height: 180, FrameRate: "24/1", BitDepth: 8, VideoRange: "SDR"}}
	root := t.TempDir()
	f.handler.PlaybackConfig = func() config.PlaybackConfig {
		return config.PlaybackConfig{TranscodeEnabled: true, HWAccel: "none", FFmpegPath: ffmpeg, TranscodeDir: root}
	}
	f.request.ClientPlaybackContext.Deliveries[playback.DeliveryClassHLSV3] = playback.DeliveryCapabilityV3{Enabled: true, SupportedOnDevice: true}
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("transcode start: %d %s", status, data)
	}
	var response playback.DecisionResponseV3
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.PlaybackPlan == nil || response.PlaybackPlan.Delivery != playback.DeliveryTranscodeHLSV3 {
		t.Fatalf("expected encoded HLS: %s", data)
	}
	sessionID := response.PlaybackPlan.SessionID
	ts := f.handler.TranscodeManager().GetTranscodeSession(sessionID)
	if ts == nil || ts.ExecutorNamespace() == nil {
		t.Fatal("missing authority-bound transcode runtime")
	}
	outputDir, err := ts.ExecutorNamespace().OutputDir(root)
	if err != nil {
		t.Fatal(err)
	}
	status, data = f.call(t, http.MethodGet, response.PlaybackPlan.Stream.URL, nil)
	if status != http.StatusOK || !bytes.Contains(data, []byte("#EXTM3U")) {
		t.Fatalf("manifest: %d %s", status, data)
	}
	currentURL := response.PlaybackPlan.Stream.URL
	for depth := 0; bytes.Contains(data, []byte("#EXTM3U")); depth++ {
		if depth >= 3 {
			t.Fatal("playlist did not resolve to media")
		}
		base, err := url.Parse(currentURL)
		if err != nil {
			t.Fatal(err)
		}
		mediaURL := ""
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				ref, err := url.Parse(line)
				if err != nil {
					t.Fatal(err)
				}
				mediaURL = base.ResolveReference(ref).String()
				break
			}
		}
		if mediaURL == "" {
			t.Fatalf("manifest has no media: %s", data)
		}
		status, data = f.call(t, http.MethodGet, mediaURL, nil)
		if status != http.StatusOK || len(data) == 0 {
			t.Fatalf("media: %d bytes=%d body=%s", status, len(data), data)
		}
		currentURL = mediaURL
	}
	for _, sample := range []map[string]any{{"sequence": 1, "position": 7}, {"sequence": 2, "position": 3}} {
		status, data = f.call(t, http.MethodPost, "/playback/"+sessionID+"/progress", sample)
		if status != 200 {
			t.Fatalf("transcode progress: %d %s", status, data)
		}
	}
	stopID := uuid.NewString()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, data = f.call(t, http.MethodDelete, "/playback/"+sessionID, map[string]any{"stop_id": stopID})
		if status == 200 {
			break
		}
		if status != 202 || time.Now().After(deadline) {
			t.Fatalf("transcode stop: %d %s", status, data)
		}
	}
	state, err := f.source.ReadPlaybackProgress(t.Context(), userstore.PlaybackProgressScope{ProfileID: f.request.ProfileID, SessionID: sessionID, MediaItemID: f.itemID})
	if err != nil || state.Stop == nil || state.Stop.Accepted == nil || state.Stop.Accepted.Sample.Sequence != 2 {
		t.Fatalf("transcode terminal: %+v %v", state, err)
	}
	if f.handler.TranscodeManager().GetTranscodeSession(sessionID) != nil {
		t.Fatal("stopped transcode remains registered")
	}
	if _, err := os.Stat(outputDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stopped executor output remains: %v", err)
	}
}

func assertInitialHTTPMutationDTO(t *testing.T, data []byte, stopID string, sequence int64, position float64) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for key := range fields {
		switch key {
		case "outcome", "accepted", "stop_id", "history_id":
		default:
			t.Fatalf("unexpected mutation response field %q: %s", key, data)
		}
	}
	var outcome string
	if err := json.Unmarshal(fields["outcome"], &outcome); err != nil || outcome == "" {
		t.Fatalf("missing lowercase outcome: %s", data)
	}
	switch outcome {
	case "applied", "replayed", "stale_sample", "stopped":
	default:
		t.Fatalf("unexpected mutation outcome %q", outcome)
	}
	var accepted map[string]json.RawMessage
	if err := json.Unmarshal(fields["accepted"], &accepted); err != nil || len(accepted) != 3 {
		t.Fatalf("invalid lowercase accepted tuple: %s", data)
	}
	var gotSequence int64
	var gotPosition float64
	var paused bool
	if err := json.Unmarshal(accepted["sequence"], &gotSequence); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(accepted["position"], &gotPosition); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(accepted["is_paused"], &paused); err != nil {
		t.Fatal(err)
	}
	if gotSequence != sequence || gotPosition != position || paused {
		t.Fatalf("accepted tuple differs: %s", data)
	}
	if stopID != "" {
		var gotStopID string
		if err := json.Unmarshal(fields["stop_id"], &gotStopID); err != nil || gotStopID != stopID {
			t.Fatalf("lowercase stop_id differs: %s", data)
		}
	}
}

type initialBeforeCommitPublishControl struct {
	InitialPlaybackControlV3
	failed   atomic.Bool
	lostRead atomic.Bool
}

func (c *initialBeforeCommitPublishControl) PublishInitialActivation(ctx context.Context, b playback.InitialActivationBindingV3, r playback.AttemptRecordV3) (playback.InitialActivationV3, error) {
	if c.failed.CompareAndSwap(false, true) {
		return playback.InitialActivationV3{}, errors.New("fixture publish failed before commit")
	}
	return c.InitialPlaybackControlV3.PublishInitialActivation(ctx, b, r)
}
func (c *initialBeforeCommitPublishControl) ReadInitialActivation(ctx context.Context, b playback.InitialActivationBindingV3) (playback.InitialActivationV3, error) {
	if c.failed.Load() && c.lostRead.CompareAndSwap(false, true) {
		return playback.InitialActivationV3{}, errors.New("fixture lost publication lookup")
	}
	return c.InitialPlaybackControlV3.ReadInitialActivation(ctx, b)
}
func TestInitialPlaybackHTTPBeforeCommitRecoveryKeepsCapabilities(t *testing.T) {
	f := newInitialHTTPFixture(t)
	c := &initialBeforeCommitPublishControl{InitialPlaybackControlV3: f.flow.Control}
	f.flow.Control = c
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusServiceUnavailable || !c.lostRead.Load() {
		t.Fatalf("expected uncertain publication: %d %s", status, data)
	}
	var phase string
	if err := f.pool.QueryRow(t.Context(), `SELECT control_activation->>'phase' FROM playback_v3_attempts WHERE playback_attempt_id=$1`, f.request.PlaybackAttemptID).Scan(&phase); err != nil || phase != "installed" {
		t.Fatalf("precommit phase %q: %v", phase, err)
	}
	status, data = f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("recovery: %d %s", status, data)
	}
	var response playback.DecisionResponseV3
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(response.ServerFeatures, "sequenced_progress_v1") || response.PlaybackPlan == nil || response.SessionID == "" {
		t.Fatalf("recovered response lost protocol: %s", data)
	}
	stored, err := c.GetAttemptByPlaybackAttemptID(t.Context(), f.request.PlaybackAttemptID)
	if err != nil || !reflect.DeepEqual(response.ServerFeatures, stored.StartResponse.ServerFeatures) || response.SessionID != stored.SessionID {
		t.Fatalf("response differs from publication: %+v %v", stored, err)
	}
	status, data = f.call(t, http.MethodPost, "/playback/"+response.SessionID+"/progress", map[string]any{"sequence": 1, "position": 17})
	if status != http.StatusOK {
		t.Fatalf("recovered sequenced progress: %d %s", status, data)
	}
}

type initialDelayedProgressProvider struct {
	userstore.PlaybackSourceProvider
	committed chan struct{}
	release   chan struct{}
}

func (p *initialDelayedProgressProvider) OpenPlaybackSink(ctx context.Context, ref userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error) {
	h, err := p.PlaybackSourceProvider.OpenPlaybackSink(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &initialDelayedProgressHandle{PlaybackSinkHandle: h, p: p}, nil
}

type initialDelayedProgressHandle struct {
	userstore.PlaybackSinkHandle
	p *initialDelayedProgressProvider
}

func (h *initialDelayedProgressHandle) ApplyPlaybackProgress(ctx context.Context, req userstore.ApplyPlaybackProgressRequest) (userstore.PlaybackProgressResult, error) {
	result, err := h.PlaybackSinkHandle.ApplyPlaybackProgress(ctx, req)
	if err == nil && req.Sample.Sequence == 1 {
		close(h.p.committed)
		select {
		case <-h.p.release:
		case <-ctx.Done():
			return userstore.PlaybackProgressResult{}, ctx.Err()
		}
	}
	return result, err
}

type initialObservedProgressControl struct {
	InitialPlaybackControlV3
	lookups atomic.Int32
	second  chan struct{}
}

func (c *initialObservedProgressControl) GetActivatedPlaybackAuthority(ctx context.Context, account int, profile, session string) (playback.ActivatedPlaybackAuthorityV3, error) {
	result, err := c.InitialPlaybackControlV3.GetActivatedPlaybackAuthority(ctx, account, profile, session)
	if err == nil && c.lookups.Add(1) == 2 {
		close(c.second)
	}
	return result, err
}
func TestInitialPlaybackHTTPLocalProgressProjectionStaysOrdered(t *testing.T) {
	f := newInitialHTTPFixture(t)
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("start: %d %s", status, data)
	}
	var response playback.DecisionResponseV3
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	p := &initialDelayedProgressProvider{PlaybackSourceProvider: f.flow.Sources, committed: make(chan struct{}), release: make(chan struct{})}
	c := &initialObservedProgressControl{InitialPlaybackControlV3: f.flow.Control, second: make(chan struct{})}
	f.flow.Sources = p
	f.flow.Control = c
	var release sync.Once
	defer release.Do(func() { close(p.release) })
	path := "/playback/" + response.SessionID + "/progress"
	first, second := make(chan int, 1), make(chan int, 1)
	go func() {
		status, _ := f.call(t, http.MethodPost, path, map[string]any{"sequence": 1, "position": 40})
		first <- status
	}()
	select {
	case <-p.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("first progress did not commit")
	}
	pendingValue, ok := f.flow.pending.Load(response.SessionID)
	if !ok {
		t.Fatal("owner missing")
	}
	pending := pendingValue.(*initialPendingPublicationV3)
	// A committed sink result must retain the same owner lock until its local
	// projection is updated; otherwise a later handler can be overwritten.
	if pending.mu.TryLock() {
		pending.mu.Unlock()
		t.Fatal("sink/local projection boundary is unlocked")
	}
	go func() {
		status, _ := f.call(t, http.MethodPost, path, map[string]any{"sequence": 2, "position": 7, "is_paused": true})
		second <- status
	}()
	select {
	case <-c.second:
	case <-time.After(5 * time.Second):
		t.Fatal("second progress did not reach authority")
	}
	release.Do(func() { close(p.release) })
	if status := <-first; status != http.StatusOK {
		t.Fatalf("first: %d", status)
	}
	if status := <-second; status != http.StatusOK {
		t.Fatalf("second: %d", status)
	}
	session, err := f.manager.GetSession(response.SessionID)
	if err != nil || session.Position != 7 || !session.IsPaused {
		t.Fatalf("local projection regressed: %+v %v", session, err)
	}
	state, err := f.source.ReadPlaybackProgress(t.Context(), userstore.PlaybackProgressScope{ProfileID: f.request.ProfileID, SessionID: response.SessionID, MediaItemID: f.itemID})
	if err != nil || state.Last == nil || state.Last.Sample.Sequence != 2 || state.Last.Sample.PositionSeconds != session.Position {
		t.Fatalf("source/local mismatch: %+v %v", state, err)
	}
}
