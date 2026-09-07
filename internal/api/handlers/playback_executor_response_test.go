package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

type nativeGrantClock struct{ elapsed atomic.Int64 }

func (c *nativeGrantClock) Now() (time.Duration, error) { return time.Duration(c.elapsed.Load()), nil }

type nativeGrantRecorder struct{ *httptest.ResponseRecorder }

func (*nativeGrantRecorder) SetWriteDeadline(time.Time) error { return nil }

func nativeBoundDirectFixture(t *testing.T, cold bool) (*StreamHandler, *http.Request, *nativeGrantClock) {
	t.Helper()
	namespace := &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
	card := playback.NewDirectRecipeCard("logical", 1, "profile", 42)
	card.Executor, card.TranscodeTransportID = namespace, "transport"
	card.RoutingWorkload, card.RoutingExecution, card.RoutingEgress = string(noderouting.WorkloadDirectPlay), string(noderouting.ExecutionNone), string(noderouting.EgressAPI)
	sessions := playback.NewSessionManager(0, 0)
	if !cold {
		sessions.RegisterReconstructed(&playback.Session{ID: card.SessionID, UserID: 1, ProfileID: "profile", MediaFileID: 42, PlayMethod: playback.PlayDirect, Executor: namespace, TranscodeTransportID: card.TranscodeTransportID,
			RoutingWorkload: card.RoutingWorkload, RoutingExecution: card.RoutingExecution, RoutingEgress: card.RoutingEgress})
	}
	file := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(file, bytes.Repeat([]byte("x"), 128*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewStreamHandler(sessions, testPlaybackFileResolver{file: &models.MediaFile{ID: 42, FilePath: file}})
	h.JWTSecret = "test-secret"
	h.TM.Sessions = sessions
	h.TM.ResolveExecutorRecipe = func(_ context.Context, transport string, ns playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		if transport != card.TranscodeTransportID || ns != *namespace {
			return nil, errors.New("wrong immutable recipe binding")
		}
		return &card, nil
	}
	clock := new(nativeGrantClock)
	h.TM.ExecuteGrants = func(ctx context.Context, transport string, ns playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		if transport != card.TranscodeTransportID || ns != *namespace {
			return nil, errors.New("wrong serve binding")
		}
		a := playback.AttemptAuthorityV3{PlaybackAttemptID: "attempt", OwnerID: "owner", Incarnation: ns.Incarnation, Epoch: ns.Epoch, State: playback.AttemptActiveV3}
		request := playback.AttemptGrantRequestV3{Executor: ns, SessionID: card.SessionID, PlanID: "plan", TransportID: transport, Purpose: purpose, Duration: time.Minute}
		return playback.AcquireRuntimeGrantV3(ctx, func(_ context.Context, authority playback.AttemptAuthorityV3, request playback.AttemptGrantRequestV3) (playback.AttemptGrantV3, error) {
			now := time.Now()
			authority.LeaseExpiresAt = now.Add(2 * time.Minute)
			return playback.AttemptGrantV3{Authority: authority, Request: request, IssuedAt: now, NotAfter: now.Add(time.Minute)}, nil
		}, clock, playback.RuntimeGrantPolicyV3{MaxDuration: time.Minute, SafetyMargin: time.Second, RenewBefore: 10 * time.Second, PollInterval: time.Millisecond}, a, request)
	}
	token, err := streamtoken.Sign(card.ToClaims(), h.JWTSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	r := playbackTestRequest(http.MethodGet, "/api/v1/stream/logical?st="+token, nil, map[string]string{"session_id": card.SessionID})
	return h, r, clock
}

func TestNativeBoundDirectLiveAndColdResponse(t *testing.T) {
	for _, cold := range []bool{false, true} {
		h, r, _ := nativeBoundDirectFixture(t, cold)
		w := &nativeGrantRecorder{httptest.NewRecorder()}
		h.HandleStream(w, r)
		if w.Code != http.StatusOK || w.Body.Len() != 128*1024 {
			t.Fatalf("cold=%v: response=%d bytes=%d", cold, w.Code, w.Body.Len())
		}
	}
}

func TestNativeBoundMetadataCannotDropSignedReference(t *testing.T) {
	h, r, _ := nativeBoundDirectFixture(t, false)
	r.URL.RawQuery = ""
	w := &nativeGrantRecorder{httptest.NewRecorder()}
	h.HandleStream(w, r)
	if w.Code != http.StatusServiceUnavailable || bytes.Contains(w.Body.Bytes(), bytes.Repeat([]byte("x"), 100)) {
		t.Fatalf("unreferenced bound metadata served media: %d", w.Code)
	}
}

func TestNativeBoundDirectRejectsStaleMetadataAndIncompleteRoutes(t *testing.T) {
	for name, mutate := range map[string]func(*playback.Session){
		"missing route":     func(s *playback.Session) { s.RoutingExecution = "" },
		"worker execution":  func(s *playback.Session) { s.RoutingExecution = "transcode_node" },
		"different file":    func(s *playback.Session) { s.MediaFileID++ },
		"different account": func(s *playback.Session) { s.UserID++ },
		"different profile": func(s *playback.Session) { s.ProfileID = "other" },
		"different method":  func(s *playback.Session) { s.PlayMethod = playback.PlayTranscode },
	} {
		t.Run(name, func(t *testing.T) {
			h, r, _ := nativeBoundDirectFixture(t, false)
			session, err := h.sessionMgr.GetSession("logical")
			if err != nil {
				t.Fatal(err)
			}
			mutate(session)
			sessions := playback.NewSessionManager(0, 0)
			sessions.RegisterReconstructed(session)
			h.sessionMgr = sessions
			h.TM.Sessions = sessions
			files := new(countingStreamFileResolver)
			h.fileResolver = files
			w := &nativeGrantRecorder{httptest.NewRecorder()}
			h.HandleStream(w, r)
			if w.Code != http.StatusServiceUnavailable || files.calls != 0 {
				t.Fatalf("stale metadata served: status=%d file calls=%d", w.Code, files.calls)
			}
		})
	}
}

func TestNativeBoundMissingFileDoesNotFinalizeLegacySession(t *testing.T) {
	h, r, _ := nativeBoundDirectFixture(t, false)
	file, err := h.fileResolver.GetByID(r.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file.FilePath); err != nil {
		t.Fatal(err)
	}
	w := &nativeGrantRecorder{httptest.NewRecorder()}
	h.HandleStream(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing file status=%d", w.Code)
	}
	if _, err := h.sessionMgr.GetSession("logical"); err != nil {
		t.Fatalf("bound error invoked legacy stop: %v", err)
	}
}

func TestNativeBoundSubtitlesRemainRefused(t *testing.T) {
	for _, endpoint := range []string{"subtitle", "fonts"} {
		t.Run(endpoint, func(t *testing.T) {
			h, original, _ := nativeBoundDirectFixture(t, false)
			r := playbackTestRequest(http.MethodGet, original.URL.String(), nil, map[string]string{"session_id": "logical", "track": "0.vtt"})
			files := new(countingStreamFileResolver)
			h.fileResolver = files
			w := httptest.NewRecorder()
			if endpoint == "subtitle" {
				h.HandleSubtitle(w, r)
			} else {
				h.HandleSubtitleFonts(w, r)
			}
			if w.Code != http.StatusServiceUnavailable || files.calls != 0 {
				t.Fatalf("unguarded subtitle delivery: status=%d lookups=%d", w.Code, files.calls)
			}
		})
	}
}

type nativeBlockedGrantWriter struct {
	header    http.Header
	entered   chan struct{}
	unblock   chan struct{}
	writeOnce sync.Once
	closeOnce sync.Once
}

func (w *nativeBlockedGrantWriter) Header() http.Header { return w.header }
func (*nativeBlockedGrantWriter) WriteHeader(int)       {}
func (*nativeBlockedGrantWriter) Flush()                {}
func (w *nativeBlockedGrantWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() && !deadline.After(time.Now()) {
		w.closeOnce.Do(func() { close(w.unblock) })
	}
	return nil
}
func (w *nativeBlockedGrantWriter) Write([]byte) (int, error) {
	w.writeOnce.Do(func() { close(w.entered) })
	<-w.unblock
	return 0, context.DeadlineExceeded
}

func TestNativeBoundDirectBlockedWriteStops(t *testing.T) {
	for _, cause := range []string{"expiry", "disconnect"} {
		t.Run(cause, func(t *testing.T) {
			h, r, clock := nativeBoundDirectFixture(t, false)
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			w := &nativeBlockedGrantWriter{header: make(http.Header), entered: make(chan struct{}), unblock: make(chan struct{})}
			done := make(chan struct{})
			go func() { defer close(done); h.HandleStream(w, r.WithContext(ctx)) }()
			select {
			case <-w.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("handler never reached media write")
			}
			if cause == "expiry" {
				clock.elapsed.Store(int64(2 * time.Minute))
			} else {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("blocked media response outlived authority")
			}
		})
	}
}

func nativeBoundHLSFixture(t *testing.T) (*PlaybackHandler, *http.Request) {
	t.Helper()
	direct, r, _ := nativeBoundDirectFixture(t, true)
	card, _ := verifiedStreamCardFromToken(r.URL.Query().Get(streamTokenParam), "logical", direct.JWTSecret)
	card.PlayMethod, card.TargetCodecVideo, card.TargetCodecAudio = playback.PlayTranscode, "libx264", "aac"
	card.RoutingWorkload, card.RoutingExecution = string(noderouting.WorkloadVideoTranscode), string(noderouting.ExecutionAPI)
	card.SegmentDuration, card.TotalDuration, card.FastStart = 2, 6, true
	direct.TM.ResolveExecutorRecipe = func(context.Context, string, playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		return card, nil
	}
	root := t.TempDir()
	binary := filepath.Join(t.TempDir(), "ffmpeg")
	script := `#!/bin/sh
cat > stream.m3u8 <<'MANIFEST'
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:2.000,
seg_00000.ts
#EXTINF:2.000,
seg_00001.ts
#EXTINF:2.000,
seg_00002.ts
MANIFEST
printf 'segment-data' > seg_00000.ts
printf 'segment-data' > seg_00001.ts
printf 'segment-data' > seg_00002.ts
exec cat >/dev/null
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	direct.TM.Config = func() playback.TranscodeRuntimeConfig {
		return playback.TranscodeRuntimeConfig{TranscodeDir: root, FFmpegPath: binary}
	}
	h := &PlaybackHandler{tm: direct.TM, sessionMgr: direct.sessionMgr, fileResolver: direct.fileResolver, JWTSecret: direct.JWTSecret}
	t.Cleanup(func() {
		if runtime := h.tm.GetTranscodeSession("logical"); runtime != nil {
			_ = runtime.Close()
		}
	})
	token, err := streamtoken.Sign(card.ToClaims(), direct.JWTSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	r = playbackTestRequest(http.MethodGet, "/api/v1/playback/transcode/logical/master.m3u8?st="+token, nil, map[string]string{"session_id": "logical", "name": "seg_00000.ts"})
	return h, r
}

func startNativeBoundHLSFixture(t *testing.T, h *PlaybackHandler, r *http.Request) {
	t.Helper()
	card, _ := verifiedStreamCardFromToken(r.URL.Query().Get(streamTokenParam), "logical", h.JWTSecret)
	cfg := h.tm.Config()
	output, err := card.Executor.OutputDir(cfg.TranscodeDir)
	if err != nil {
		t.Fatal(err)
	}
	opts := card.TranscodeOpts(output, cfg.FFmpegPath, nil)
	opts.HWAccel = playback.HWAccelNone
	opts.ExecuteGrants = h.tm.ExecuteGrants
	runtime, err := playback.StartTranscode(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !h.tm.SwapTranscodeSessionIf("logical", nil, runtime) {
		_ = runtime.Close()
		t.Fatal("register prepared initial runtime")
	}
	if _, err := runtime.WaitForManifest(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestNativeBoundHLSColdAndLiveGuardedChain(t *testing.T) {
	h, r := nativeBoundHLSFixture(t)
	cold := &nativeGrantRecorder{httptest.NewRecorder()}
	h.HandleGetTranscodeManifest(cold, r)
	if cold.Code != http.StatusServiceUnavailable || h.tm.GetTranscodeSession("logical") != nil {
		t.Fatal("cold bound request reconstructed executor")
	}
	startNativeBoundHLSFixture(t, h, r)
	for _, phase := range []string{"live", "repeat"} {
		w := &nativeGrantRecorder{httptest.NewRecorder()}
		h.HandleGetTranscodeManifest(w, r)
		if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("#EXTM3U")) {
			t.Fatalf("%s manifest=%d %s", phase, w.Code, w.Body.String())
		}
		runtime := h.tm.GetTranscodeSession("logical")
		if runtime == nil {
			t.Fatal("guarded chain did not start executor")
		}
		if _, err := runtime.WaitForManifest(time.Second); err != nil {
			t.Fatal(err)
		}
		segment := &nativeGrantRecorder{httptest.NewRecorder()}
		h.HandleGetTranscodeSegment(segment, r)
		if segment.Code != http.StatusOK || segment.Body.String() != "segment-data" {
			t.Fatalf("%s segment=%d %q", phase, segment.Code, segment.Body.String())
		}
	}
}

func TestNativeBoundHLSDeniedExecuteGrantRemainsUnavailable(t *testing.T) {
	for _, endpoint := range []string{"manifest", "segment"} {
		t.Run(endpoint, func(t *testing.T) {
			h, r := nativeBoundHLSFixture(t)
			provider := h.tm.ExecuteGrants
			h.tm.ExecuteGrants = func(ctx context.Context, transport string, ns playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
				if purpose == playback.AttemptGrantExecuteV3 {
					return nil, playback.ErrRuntimeGrantExpiredV3
				}
				return provider(ctx, transport, ns, purpose)
			}
			w := &nativeGrantRecorder{httptest.NewRecorder()}
			if endpoint == "manifest" {
				h.HandleGetTranscodeManifest(w, r)
			} else {
				h.HandleGetTranscodeSegment(w, r)
			}
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("denied execution=%d %s", w.Code, w.Body.String())
			}
			if runtime := h.tm.GetTranscodeSession("logical"); runtime != nil {
				t.Fatal("denied execution spawned runtime")
			}
		})
	}
}

func TestNativeBoundLegacyLifecycleDoesNotMutateSession(t *testing.T) {
	direct, _, _ := nativeBoundDirectFixture(t, false)
	h := &PlaybackHandler{sessionMgr: direct.sessionMgr, tm: direct.TM}
	for _, operation := range []string{"progress", "stop"} {
		w := httptest.NewRecorder()
		r := playbackTestRequest(http.MethodPost, "/api/v1/playback/logical/progress", []byte(`{"position":120,"is_paused":true}`), map[string]string{"session_id": "logical"})
		if operation == "progress" {
			h.HandleUpdateProgress(w, r)
		} else {
			h.HandleStopPlayback(w, r)
		}
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status=%d", operation, w.Code)
		}
	}
	session, err := direct.sessionMgr.GetSession("logical")
	if err != nil || session.Position != 0 || session.IsPaused {
		t.Fatalf("legacy lifecycle mutated bound session: %+v %v", session, err)
	}
	if err := h.stopPlaybackSessionByID(t.Context(), session.ID, true); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("shared stop accepted bound session: %v", err)
	}
	if err := h.abortPlaybackSession(t.Context(), session); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("shared abort accepted bound session: %v", err)
	}
	if _, err := direct.sessionMgr.GetSession("logical"); err != nil {
		t.Fatalf("shared lifecycle removed bound session: %v", err)
	}
}

func TestNativeBoundLegacyFinalizersDoNotPersist(t *testing.T) {
	direct, _, _ := nativeBoundDirectFixture(t, false)
	session, err := direct.sessionMgr.GetSession("logical")
	if err != nil {
		t.Fatal(err)
	}
	session.Position = 120
	store := newPlaybackTestStore(t)
	admin := new(recordingPlaybackAdminStore)
	scrobble := new(recordingPlaybackWatchScrobbler)
	h := &PlaybackHandler{sessionMgr: direct.sessionMgr, tm: direct.TM,
		fileResolver:  testPlaybackFileResolver{file: &models.MediaFile{ID: 42, ContentID: "movie", Duration: 3600}},
		StoreProvider: testUserStoreProvider{store: store}, AdminStore: admin, WatchScrobbler: scrobble}
	h.persistProgress(t.Context(), session)
	h.persistStopAndHistory(t.Context(), session)
	h.finalizeSessionStop(t.Context(), session, true, "test", true)
	h.finalizeSessionAbort(t.Context(), session, true, "test")
	h.handleExpiredSession(session)
	direct.AdminStore = admin
	direct.finalizeSessionAbort(t.Context(), session, true, "test")
	direct.abortPlaybackSession(t.Context(), session)
	progress, err := store.GetProgress(t.Context(), session.ProfileID, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if progress != nil {
		t.Fatalf("legacy bound progress persisted: %+v", progress)
	}
	history, err := store.ListHistory(t.Context(), session.ProfileID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 || len(admin.history) != 0 || len(admin.deleted) != 0 || len(scrobble.stops) != 0 || len(scrobble.pauses) != 0 {
		t.Fatal("legacy bound finalization produced side effects")
	}
	if _, err := direct.sessionMgr.GetSession(session.ID); err != nil {
		t.Fatalf("bound abort stopped metadata: %v", err)
	}
}

func TestNativeBoundRuntimeCannotDowngradeLegacyLifecycle(t *testing.T) {
	h, r := nativeBoundHLSFixture(t)
	startNativeBoundHLSFixture(t, h, r)
	manifest := &nativeGrantRecorder{httptest.NewRecorder()}
	h.HandleGetTranscodeManifest(manifest, r)
	if manifest.Code != http.StatusOK {
		t.Fatalf("start bound runtime: %d", manifest.Code)
	}
	session, err := h.sessionMgr.GetSession("logical")
	if err != nil {
		t.Fatal(err)
	}
	session.Executor = nil
	session.Position = 120
	sessions := playback.NewSessionManager(0, 0)
	sessions.RegisterReconstructed(session)
	h.sessionMgr, h.tm.Sessions = sessions, sessions
	if !nativeSessionExecutorBound(h.tm, session) {
		t.Fatal("stripped metadata hid bound runtime")
	}
	for _, operation := range []string{"progress", "stop"} {
		w := httptest.NewRecorder()
		req := playbackTestRequest(http.MethodPost, "/api/v1/playback/logical/progress", []byte(`{"position":999,"is_paused":true}`), map[string]string{"session_id": "logical"})
		if operation == "progress" {
			h.HandleUpdateProgress(w, req)
		} else {
			h.HandleStopPlayback(w, req)
		}
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s accepted stripped metadata: %d", operation, w.Code)
		}
	}
	if err := h.stopPlaybackSessionByID(t.Context(), session.ID, true); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("shared stop accepted downgrade: %v", err)
	}
	if err := h.abortPlaybackSession(t.Context(), session); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("shared abort accepted downgrade: %v", err)
	}
	store := newPlaybackTestStore(t)
	admin := new(recordingPlaybackAdminStore)
	h.StoreProvider, h.AdminStore = testUserStoreProvider{store: store}, admin
	h.fileResolver = testPlaybackFileResolver{file: &models.MediaFile{ID: 42, ContentID: "movie", Duration: 3600}}
	h.persistProgress(t.Context(), session)
	h.persistStopAndHistory(t.Context(), session)
	h.finalizeSessionStop(t.Context(), session, true, "test", true)
	h.finalizeSessionAbort(t.Context(), session, true, "test")
	h.handleExpiredSession(session)
	stream := NewStreamHandler(sessions, new(countingStreamFileResolver))
	stream.TM, stream.AdminStore = h.tm, admin
	for _, endpoint := range []string{"subtitle", "fonts"} {
		w := httptest.NewRecorder()
		req := playbackTestRequest(http.MethodGet, "/subtitles/0.vtt", nil, map[string]string{"session_id": "logical", "track": "0.vtt"})
		if endpoint == "subtitle" {
			stream.HandleSubtitle(w, req)
		} else {
			stream.HandleSubtitleFonts(w, req)
		}
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s accepted downgrade: %d", endpoint, w.Code)
		}
	}
	stream.finalizeSessionAbort(t.Context(), session, true, "test")
	stream.abortPlaybackSession(t.Context(), session)
	progress, err := store.GetProgress(t.Context(), session.ProfileID, "movie")
	if err != nil || progress != nil {
		t.Fatalf("downgraded progress persisted: %+v %v", progress, err)
	}
	history, err := store.ListHistory(t.Context(), session.ProfileID, 10, 0)
	if err != nil || len(history) != 0 || len(admin.history) != 0 || len(admin.deleted) != 0 {
		t.Fatalf("downgraded finalization wrote history: %v", err)
	}
	current, err := sessions.GetSession(session.ID)
	if err != nil || current.Position != 120 || current.IsPaused {
		t.Fatalf("downgraded lifecycle mutated session: %+v %v", current, err)
	}
}

func TestNativeBoundWorkerTransferUsesSelectedAPIEgress(t *testing.T) {
	for _, mode := range []string{"valid", "missing transfer", "wrong egress", "redirect", "not modified"} {
		t.Run(mode, func(t *testing.T) {
			stream, original, _ := nativeBoundDirectFixture(t, true)
			ref, _ := verifiedStreamCardFromToken(original.URL.Query().Get("st"), "logical", stream.JWTSecret)
			card, err := stream.TM.ResolveExecutorRecipe(t.Context(), "transport", *ref.Executor)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get(playback.OutputTransferHeaderV3) != "permit" {
					t.Error("missing permit")
				}
				claims, err := streamtoken.Verify(r.Header.Get("X-Silo-Stream-Token"), stream.JWTSecret)
				if err != nil || claims.ExecutorID != card.Executor.ExecutorID {
					t.Error("missing executor reference")
				}
				if mode == "not modified" {
					if r.Header.Get("If-None-Match") != "\"etag\"" {
						t.Error("conditional validator not forwarded")
					}
					w.Header().Set("ETag", "\"etag\"")
					w.Header().Set("X-Silo-Transcode-Segment-Generation", "generation")
					w.WriteHeader(http.StatusNotModified)
					return
				}

				if mode == "redirect" {
					http.Redirect(w, r, "/unexpected", 307)
					return
				}
				w.Header().Set(playback.OutputTransferHeaderV3, "private")
				_, _ = w.Write([]byte("media"))
			}))
			defer worker.Close()
			card.PlayMethod = playback.PlayTranscode
			card.RoutingWorkload = string(noderouting.WorkloadVideoTranscode)
			card.RoutingExecution = string(noderouting.ExecutionTranscode)
			card.RoutingExecutionNodeID = 1
			card.TranscodeNodeURL = worker.URL
			if mode == "wrong egress" {
				card.RoutingEgressNodeID = 2
				card.RoutingEgress = string(noderouting.EgressProxy)
			}
			closed := make(chan struct{}, 1)
			if mode != "missing transfer" {
				stream.TM.OpenOutputTransfer = func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3) (string, func(), error) {
					if transport != "transport" || executor != *card.Executor {
						t.Error("wrong transfer identity")
					}
					return "permit", func() { closed <- struct{}{} }, nil
				}
			}
			h := &PlaybackHandler{tm: stream.TM, JWTSecret: stream.JWTSecret}
			router := chi.NewRouter()
			router.Get("/{session_id}", func(w http.ResponseWriter, r *http.Request) {
				w, r, cleanup, ok := guardNativeExecutorResponse(w, r, stream.TM, stream.sessionMgr.GetSession, "logical", stream.JWTSecret)
				if !ok {
					return
				}
				defer cleanup()
				h.proxyToTranscodeNode(w, r, worker.URL, "/transcode/transport/segment/seg_00001.ts")
			})
			server := httptest.NewServer(router)
			defer server.Close()
			token, err := streamtoken.Sign(card.ToClaims(), stream.JWTSecret, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/logical?st="+token, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("If-None-Match", "\"etag\"")
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.Header.Get(playback.OutputTransferHeaderV3) != "" {
				t.Fatal("permit escaped")
			}
			switch mode {
			case "valid":
				if response.StatusCode != 200 || string(body) != "media" {
					t.Fatalf("status=%d body=%q", response.StatusCode, body)
				}
			case "not modified":
				if response.StatusCode != http.StatusNotModified || len(body) != 0 || response.Header.Get("ETag") != "\"etag\"" {
					t.Fatalf("conditional status=%d body=%q", response.StatusCode, body)
				}

			case "redirect":
				if response.StatusCode != 502 || response.Header.Get("Location") != "" {
					t.Fatal("redirect escaped")
				}
			default:
				if response.StatusCode != 503 || calls.Load() != 0 {
					t.Fatalf("invalid authority reached worker status=%d calls=%d", response.StatusCode, calls.Load())
				}
			}
			if mode == "valid" || mode == "redirect" || mode == "not modified" {
				if calls.Load() != 1 {
					t.Fatal("worker replayed")
				}
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("permit not closed")
				}
			}
		})
	}
}
