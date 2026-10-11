package transcodenode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

const nativeExecutionTestLocation = "bloem-storage:synthetic-pinned-catalog-identity"

func TestNativeExecutionWrapperNeverTreatsStartBodyAsGrant(t *testing.T) {
	server := newTestServer(t)
	called, served := false, false
	wrapped := server.WrapNativeExecution(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true }), NativeExecutionConfig{NodeID: 7, Resolve: func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error) {
		called = true
		return nil, errors.New("grant denied")
	}})
	body, _ := json.Marshal(TranscodeStartRequest{SessionID: "transport", InputPath: nativeExecutionTestLocation})
	request := httptest.NewRequest(http.MethodPost, "/transcode/start", bytes.NewReader(body))
	response := httptest.NewRecorder()
	wrapped.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || called || served {
		t.Fatal("unauthenticated start reached grant resolver/executor")
	}
	request = httptest.NewRequest(http.MethodPost, "/transcode/start", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testSecret)
	response = httptest.NewRecorder()
	wrapped.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !called || served {
		t.Fatal("unsigned body became source authority")
	}
}

func TestNativeExecutionWrapperChecksGrantAndBindsOnlyExactPath(t *testing.T) {
	for _, bad := range []string{"worker", "transport", "path", "revision", "session", "expiry", "factory", ""} {
		t.Run(bad, func(t *testing.T) {
			server := newTestServer(t)
			served := false
			source := &playback.TranscodeInputSource{Open: func(context.Context, playback.TranscodeOpts) (playback.TranscodeInput, error) {
				return nil, errors.New("not opened in binding test")
			}}
			grant := &NativeExecutionGrant{WorkerID: 7, PlaybackSessionID: "playback-session", TransportID: "transport", InputPath: nativeExecutionTestLocation, Revision: "immutable-version", InputFormat: "mov", InputSource: source, ExpiresAt: time.Now().Add(time.Minute)}
			switch bad {
			case "worker":
				grant.WorkerID++
			case "transport":
				grant.TransportID = "another"
			case "path":
				grant.InputPath += "-changed"
			case "revision":
				grant.Revision = ""
			case "session":
				grant.PlaybackSessionID = ""
			case "expiry":
				grant.ExpiresAt = time.Now().Add(-time.Second)
			case "factory":
				grant.InputSource = nil
			}
			wrapped := server.WrapNativeExecution(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				served = true
				if !playback.TranscodeInputSourceMatches(r.Context(), nativeExecutionTestLocation) || playback.TranscodeInputSourceMatches(r.Context(), nativeExecutionTestLocation+"-changed") {
					t.Error("grant escaped exact location")
				}
			}), NativeExecutionConfig{NodeID: 7, Resolve: func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error) { return grant, nil }})
			body, _ := json.Marshal(TranscodeStartRequest{SessionID: "transport", InputPath: nativeExecutionTestLocation})
			request := httptest.NewRequest(http.MethodPost, "/transcode/start", bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+testSecret)
			response := httptest.NewRecorder()
			wrapped.ServeHTTP(response, request)
			if served != (bad == "") {
				t.Fatalf("grant %q admitted=%v", bad, served)
			}
		})
	}
}

func TestNativeInputPathAuthorizerPreservesLocalAuthority(t *testing.T) {
	paths := NewNativeInputPathAuthorizer(allowInputPaths{})
	if allowed, err := paths.Allowed(context.Background(), "/ordinary/catalog/file.mp4"); err != nil || !allowed {
		t.Fatal("ordinary authority changed")
	}
	if allowed, err := paths.Allowed(context.Background(), nativeExecutionTestLocation); err != nil || allowed {
		t.Fatal("native identity admitted without grant")
	}
}

func TestNativeExecutionProgressiveRemuxUsesExistingDurableStopFence(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "source.mp4")
	if output, err := exec.Command(bin, "-v", "error", "-f", "lavfi", "-i", "color=c=green:s=128x96:r=24", "-t", "3", "-c:v", "libx264", "-threads", "1", "-g", "24", "-pix_fmt", "yuv420p", "-movflags", "+faststart", path).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	server := newTestServer(t)
	server.nodeRowID = func() (int, bool) { return 7, true }
	server.watcher.Config().Playback.FFmpegPath = bin
	claims := streamtoken.Claims{SessionID: "player", TranscodeTransportID: "transport", MediaPath: nativeExecutionTestLocation, PlayMethod: string(playback.PlayRemux), TranscodeNode: "synthetic-node", RoutingWorkload: "remux", RoutingExecution: "transcode", RoutingExecutionNodeID: 7, RoutingEgress: "proxy", RemuxDVMode: string(playback.RemuxDVPreserveV3)}
	card := playback.RecipeCardFromClaims(&claims)
	store := &stubRecipeStore{card: &card, ok: true}
	server.SetRecipeStore(store)
	var opens, releases atomic.Int32
	source := &playback.TranscodeInputSource{Open: func(ctx context.Context, _ playback.TranscodeOpts) (playback.TranscodeInput, error) {
		lifetime, cancel := context.WithCancel(ctx)
		file, err := mediasource.OpenLocal(lifetime, path)
		if err != nil {
			cancel()
			return nil, err
		}
		input, err := nativestorage.NewToolInput(lifetime, file, nativestorage.ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, ReleaseHost: func() { releases.Add(1) }, MaxBytes: 8 << 20, ChunkBytes: 4096, MaxConcurrentReads: 2, Timeout: 10 * time.Second})
		if err != nil {
			cancel()
			_ = file.Close()
			return nil, err
		}
		opens.Add(1)
		return input, nil
	}}
	wrapped := server.WrapNativeExecution(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("native remux reached filesystem handler") }), NativeExecutionConfig{NodeID: 7, Resolve: func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error) {
		return &NativeExecutionGrant{WorkerID: 7, PlaybackSessionID: "player", TransportID: "transport", InputPath: nativeExecutionTestLocation, Revision: "immutable", InputFormat: "mov", InputSource: source, ExpiresAt: time.Now().Add(time.Minute)}, nil
	}})
	token, err := streamtoken.Sign(claims, testSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	serve := func(method string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/remux/transport", nil)
		request.Header.Set("Authorization", "Bearer "+testSecret)
		request.Header.Set("X-Silo-Stream-Token", token)
		response := httptest.NewRecorder()
		wrapped.ServeHTTP(response, request)
		return response
	}
	response := serve(http.MethodGet)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("ftyp")) {
		t.Fatalf("progressive response %d: %s", response.Code, response.Body.String())
	}
	if opens.Load() != 1 || releases.Load() != 1 {
		t.Fatal("progressive source did not join")
	}
	response = serve(http.MethodHead)
	if response.Code != http.StatusOK || response.Body.Len() != 0 || opens.Load() != 2 || releases.Load() != 2 {
		t.Fatal("HEAD did not freshly authorize/join immutable source")
	}
	_ = store.Delete(context.Background(), "transport")
	response = serve(http.MethodGet)
	if response.Code != http.StatusGone || opens.Load() != 2 {
		t.Fatal("deleted durable authority allowed remux restart")
	}
}
