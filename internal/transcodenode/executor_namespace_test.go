package transcodenode

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func workerNamespace() *playback.ExecutorNamespaceV3 {
	return &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
}
func workerBoundSession(t *testing.T, s *Server, ns *playback.ExecutorNamespaceV3) *playback.TranscodeSession {
	t.Helper()
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := ns.OutputDir(s.transcodeDir)
	if err != nil {
		t.Fatal(err)
	}
	session, err := playback.StartTranscode(t.Context(), playback.TranscodeOpts{Executor: ns, OutputDir: dir, SessionID: "worker-session", InputPath: "/media/movie.mkv", FFmpegPath: bin, HWAccel: playback.HWAccelNone, TargetCodecVideo: "h264", SegmentDuration: 6})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
func workerRequest(t *testing.T, ns *playback.ExecutorNamespaceV3) *http.Request {
	t.Helper()
	card := transcodeCard("worker-session")
	card.Executor = ns
	return requestWithToken("worker-session", signCard(t, card))
}

func TestWorkerExecutorSessionReference(t *testing.T) {
	s := newTestServer(t)
	ns := workerNamespace()
	session := workerBoundSession(t, s, ns)
	s.sessions["worker-session"] = session
	for _, expected := range []*playback.ExecutorNamespaceV3{nil, workerNamespace()} {
		if _, _, err := s.acquireExecutorSession(workerRequest(t, expected), "worker-session"); err == nil {
			t.Fatal("missing/mismatched executor acquired session")
		}
	}
	if got, ok, err := s.acquireExecutorSession(workerRequest(t, ns), "worker-session"); err != nil || !ok || got != session {
		t.Fatalf("matching reference: %v %v", ok, err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/transcode/worker-session", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("session_id", "worker-session")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	rr := httptest.NewRecorder()
	s.handleStop(rr, req)
	if rr.Code != http.StatusConflict || s.sessions["worker-session"] != session {
		t.Fatal("legacy stop controlled bound executor")
	}
}

func TestWorkerExecutorStartRejectsDowngradeAndInvalidNamespace(t *testing.T) {
	s := newTestServer(t)
	ns := workerNamespace()
	session := workerBoundSession(t, s, ns)
	s.sessions["worker-session"] = session
	for _, incoming := range []*playback.ExecutorNamespaceV3{nil, workerNamespace(), ns, {Incarnation: "../escape", Epoch: 1, ExecutorID: uuid.NewString()}} {
		body, _ := json.Marshal(TranscodeStartRequest{SessionID: "worker-session", InputPath: "/media/movie.mkv", Executor: incoming, TargetCodecVideo: "h264", SegmentDuration: 6})
		rr := httptest.NewRecorder()
		s.handleStart(rr, httptest.NewRequest(http.MethodPost, "/transcode/start", bytes.NewReader(body)))
		if rr.Code != http.StatusConflict && rr.Code != http.StatusBadRequest {
			t.Fatalf("replacement status %d: %s", rr.Code, rr.Body.String())
		}
		if s.sessions["worker-session"] != session {
			t.Fatal("rejected replacement removed predecessor")
		}
	}
}

func TestWorkerExecutorStaleCleanupPreservesSuccessorAndLegacyDirectory(t *testing.T) {
	s := newTestServer(t)
	oldNS := workerNamespace()
	nextNS := workerNamespace()
	old := workerBoundSession(t, s, oldNS)
	next := workerBoundSession(t, s, nextNS)
	legacy := s.sessionOutputDir("worker-session")
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	oldDir, _ := oldNS.OutputDir(s.transcodeDir)
	nextDir, _ := nextNS.OutputDir(s.transcodeDir)
	for _, dir := range []string{oldDir, nextDir, legacy} {
		if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.sessions["worker-session"] = next
	s.lastAccess["worker-session"] = time.Now().Add(-time.Hour)
	s.reapSession("worker-session", old, time.Now())
	if s.sessions["worker-session"] != next {
		t.Fatal("stale reaper removed successor")
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{nextDir, legacy} {
		if _, err := os.Stat(filepath.Join(dir, "sentinel")); err != nil {
			t.Fatalf("stale cleanup removed %s: %v", dir, err)
		}
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old namespace not cleaned: %v", err)
	}
	if err := s.teardownForForceReload(t.Context(), "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "sentinel")); err != nil {
		t.Fatalf("bound reload removed stable session directory: %v", err)
	}
	if _, err := os.Stat(nextDir); !os.IsNotExist(err) {
		t.Fatalf("bound reload left victim directory: %v", err)
	}
}

func TestWorkerExecutorReconstructionRequiresResolver(t *testing.T) {
	s := newTestServer(t)
	ns := workerNamespace()
	card := transcodeCard("worker-session")
	card.Executor = ns
	legacy := &stubRecipeStore{ok: true, card: &card}
	s.SetRecipeStore(legacy)
	if session, _ := s.reconstructFromToken(workerRequest(t, ns), "worker-session", -1); session != nil {
		t.Fatal("complete bound token reconstructed without current resolver")
	}
	if legacy.hits != 0 {
		t.Fatal("bound token fell back to legacy recipe locator")
	}
	calls := 0
	s.WithExecutorRecipeResolver(func(_ context.Context, _ string, _ playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		calls++
		wrong := card
		wrong.Executor = workerNamespace()
		return &wrong, nil
	})
	if session, _ := s.reconstructFromToken(workerRequest(t, ns), "worker-session", -1); session != nil {
		t.Fatal("mismatched resolver recipe reconstructed")
	}
	if calls != 1 || legacy.hits != 0 {
		t.Fatal("wrong recipe resolution path")
	}
}

func TestWorkerExecutorResolvedReconstructionUsesOwnLeaf(t *testing.T) {
	s := newTestServer(t)
	s.tracker = &recordingSessionTracker{}
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	s.watcher.Config().Playback.FFmpegPath = bin
	s.watcher.Config().Playback.HWAccel = playback.HWAccelNone
	ns := workerNamespace()
	card := transcodeCard("worker-session")
	card.Executor = ns
	legacy := &stubRecipeStore{ok: true, card: &card}
	s.SetRecipeStore(legacy)
	calls := 0
	s.WithExecutorRecipeResolver(func(_ context.Context, id string, expected playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		calls++
		if id != "worker-session" || expected != *ns {
			t.Fatal("incorrect resolver identity")
		}
		return &card, nil
	})
	session, err := s.reconstructFromToken(workerRequest(t, ns), "worker-session", -1)
	if err != nil || session == nil {
		t.Fatalf("resolved reconstruction: %v", err)
	}
	defer func() { _ = session.Close() }()
	if calls != 1 || legacy.hits != 0 {
		t.Fatalf("resolver=%d legacy=%d", calls, legacy.hits)
	}
	dir, _ := ns.OutputDir(s.transcodeDir)
	if session.Opts().OutputDir != dir {
		t.Fatalf("output=%q want=%q", session.Opts().OutputDir, dir)
	}
	if _, err := os.Stat(s.sessionOutputDir("worker-session")); !os.IsNotExist(err) {
		t.Fatalf("bound reconstruct touched legacy output: %v", err)
	}
	// Closing this reconstruction must not delete a future executor leaf, even
	// when both executors belong to the same public transport session.
	nextNS := workerNamespace()
	next := workerBoundSession(t, s, nextNS)
	defer func() { _ = next.Close() }()
	nextDir, _ := nextNS.OutputDir(s.transcodeDir)
	if err := os.WriteFile(filepath.Join(nextDir, "sentinel"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(nextDir, "sentinel")); err != nil {
		t.Fatalf("reconstruction cleanup deleted successor: %v", err)
	}
}

func TestWorkerExecutorProgressiveRemuxFailsClosed(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleRemux(rr, workerRequest(t, workerNamespace()))
	if rr.Code != http.StatusConflict {
		t.Fatalf("bound remux status=%d", rr.Code)
	}
}
