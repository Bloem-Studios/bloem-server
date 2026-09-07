package apiv2

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/google/uuid"
)

type fontLifetimeClock struct{ elapsed atomic.Int64 }

func (c *fontLifetimeClock) Now() (time.Duration, error) { return time.Duration(c.elapsed.Load()), nil }

type fontLifetimeFile struct{ file *models.MediaFile }

func (f fontLifetimeFile) GetByID(_ context.Context, id int) (*models.MediaFile, error) {
	if id != f.file.ID {
		return nil, errors.New("unexpected file")
	}
	return f.file, nil
}

type observedFontService struct {
	inner PlaybackSubtitleFontService
	after func()
}

func (s observedFontService) BoundSubtitleFontBundle(w http.ResponseWriter, r *http.Request) ([]playback.SubtitleFontBundleItem, error) {
	items, err := s.inner.BoundSubtitleFontBundle(w, r)
	if s.after != nil {
		s.after()
	}
	return items, err
}

// Generate a container with synthetic attachment bytes: font rendering is not
// under test, but both probe and attachment extraction run real ffmpeg tools.
func fontLifetimeMedia(t *testing.T) (string, string, []byte) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("ffmpeg required for retained HTTP evidence")
	}
	dir := t.TempDir()
	ass := filepath.Join(dir, "cue.ass")
	if err := os.WriteFile(ass, []byte("[Script Info]\nScriptType: v4.00+\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,synthetic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	font := bytes.Repeat([]byte{0, 1, 2, 3}, 32768)
	attachment := filepath.Join(dir, "synthetic.ttf")
	if err := os.WriteFile(attachment, font, 0600); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "fixture.mkv")
	out, err := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=size=16x16:rate=1", "-i", ass, "-attach", attachment, "-metadata:s:t:0", "mimetype=font/ttf", "-metadata:s:t:0", "filename=synthetic.ttf", "-t", "1", "-map", "0:v", "-map", "1:s", "-c:v", "ffv1", "-c:s", "ass", "-y", media).CombinedOutput()
	if err != nil {
		t.Fatalf("generate fixture: %v %s", err, out)
	}
	return media, ffmpeg, font
}

type fontLifetimeFixture struct {
	handler http.Handler
	path    string
	clock   *fontLifetimeClock
	first   atomic.Pointer[playback.RuntimeGrantV3]
	grants  atomic.Int64
}

func newFontLifetimeFixture(t *testing.T, media, ffmpeg, mode string, after func(*fontLifetimeFixture)) *fontLifetimeFixture {
	t.Helper()
	f := &fontLifetimeFixture{clock: new(fontLifetimeClock)}
	ns := &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
	card := playback.NewDirectRecipeCard(deliveryTestSession, 1, "p-owner", 42)
	card.Executor, card.TranscodeTransportID = ns, "font-transport"
	card.RoutingWorkload, card.RoutingExecution, card.RoutingEgress = "direct_play", "none", "api"
	switch mode {
	case "proxy":
		card.RoutingEgress = "proxy"
	case "encoded-api":
		card.PlayMethod = playback.PlayTranscode
		card.RoutingWorkload, card.RoutingExecution = "video_transcode", "api"
	case "remote-api":
		card.PlayMethod = playback.PlayTranscode
		card.RoutingWorkload, card.RoutingExecution = "video_transcode", "worker"
		card.TranscodeNodeURL = "https://worker.example.test"
	case "foreign-account":
		card.UserID = 2
	case "foreign-profile":
		card.ProfileID = "different-profile"
	}
	sessions := playback.NewSessionManager(0, 0)
	sessions.RegisterReconstructed(&playback.Session{ID: card.SessionID, UserID: card.UserID, ProfileID: card.ProfileID, MediaFileID: 42, PlayMethod: card.PlayMethod, Executor: ns, TranscodeTransportID: card.TranscodeTransportID, TranscodeNodeURL: card.TranscodeNodeURL, RoutingWorkload: card.RoutingWorkload, RoutingExecution: card.RoutingExecution, RoutingEgress: card.RoutingEgress})
	h := handlers.NewStreamHandler(sessions, fontLifetimeFile{&models.MediaFile{ID: 42, FilePath: media, SubtitleTracks: []models.SubtitleTrack{{Index: 1, Codec: "ass"}}}})
	h.JWTSecret = "synthetic-test-secret"
	h.PlaybackConfig = func() config.PlaybackConfig { return config.PlaybackConfig{FFmpegPath: ffmpeg} }
	h.TM.ResolveExecutorRecipe = func(_ context.Context, transport string, executor playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		if transport != card.TranscodeTransportID || executor != *ns {
			return nil, errors.New("wrong recipe binding")
		}
		return &card, nil
	}
	h.TM.ExecuteGrants = func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		if transport != card.TranscodeTransportID || executor != *ns || purpose != playback.AttemptGrantServeV3 {
			return nil, errors.New("wrong grant binding")
		}
		a := playback.AttemptAuthorityV3{PlaybackAttemptID: "attempt", OwnerID: "owner", Incarnation: ns.Incarnation, Epoch: 1, State: playback.AttemptActiveV3}
		request := playback.AttemptGrantRequestV3{Executor: *ns, SessionID: card.SessionID, PlanID: "plan", TransportID: transport, Purpose: purpose, Duration: time.Minute}
		grant, err := playback.AcquireRuntimeGrantV3(ctx, func(_ context.Context, a playback.AttemptAuthorityV3, r playback.AttemptGrantRequestV3) (playback.AttemptGrantV3, error) {
			now := time.Now()
			a.LeaseExpiresAt = now.Add(2 * time.Minute)
			return playback.AttemptGrantV3{Authority: a, Request: r, IssuedAt: now, NotAfter: now.Add(time.Minute)}, nil
		}, f.clock, playback.RuntimeGrantPolicyV3{MaxDuration: time.Minute, SafetyMargin: time.Second, RenewBefore: 10 * time.Second, PollInterval: time.Millisecond}, a, request)
		if err == nil {
			f.grants.Add(1)
			f.first.CompareAndSwap(nil, grant)
		}
		return grant, err
	}
	token, err := streamtoken.Sign(card.ToClaims(), h.JWTSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "bad-signature" {
		token += "invalid"
	}
	if mode == "expired-reference" {
		token, err = streamtoken.Sign(card.ToClaims(), h.JWTSecret, -time.Minute)
		if err != nil {
			t.Fatal(err)
		}
	}
	f.path = Prefix + "/stream/" + deliveryTestSession + "/subtitles/0/fonts?file_id=42&st=" + url.QueryEscape(token)
	deps, _ := catalogDeps(t)
	deps.PlaybackMedia = &PlaybackMediaHandlers{SubtitleFonts: observedFontService{inner: h, after: func() {
		if after != nil {
			after(f)
		}
	}}}
	f.handler = newTestHandler(t, deps)
	return f
}

// Pause the actual transport body write, not the producer or Huma marshal.
// Deadlines still reach net/http, so expiry interrupts the eventual write.
type pausedFontWriter struct {
	http.ResponseWriter
	request *http.Request
	entered chan struct{}
	expired chan struct{}
	release <-chan struct{}
}

func (w *pausedFontWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *pausedFontWriter) SetWriteDeadline(deadline time.Time) error {
	err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
	if err == nil && !deadline.IsZero() && !deadline.After(time.Now()) {
		select {
		case w.expired <- struct{}{}:
		default:
		}
	}
	return err
}
func (w *pausedFontWriter) Write(data []byte) (int, error) {
	close(w.entered)
	select {
	case <-w.release:
	case <-w.request.Context().Done():
		return 0, w.request.Context().Err()
	}
	return w.ResponseWriter.Write(data)
}
func fontRequest(t *testing.T, ctx context.Context, target string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range viewerHeaders() {
		r.Header.Set(k, v)
	}
	return r
}
func awaitFontSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("font HTTP lifecycle did not reach observable boundary")
	}
}

func TestSubtitleFontResponseHTTPWholeLifetime(t *testing.T) {
	media, ffmpeg, font := fontLifetimeMedia(t)
	for _, mode := range []string{"slow-success", "cancel", "expire-during-write"} {
		t.Run(mode, func(t *testing.T) {
			f := newFontLifetimeFixture(t, media, ffmpeg, "", func(f *fontLifetimeFixture) {
				if g := f.first.Load(); g == nil || g.Check() != nil {
					t.Error("producer returned without live serving authority for final JSON")
				}
			})
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			expired := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				f.handler.ServeHTTP(&pausedFontWriter{ResponseWriter: w, request: r, entered: entered, expired: expired, release: release}, r)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := server.Client()
			client.Timeout = 5 * time.Second
			response, err := client.Do(fontRequest(t, ctx, server.URL+f.path))
			if err != nil {
				t.Fatalf("HTTP request failed: %v", errors.Unwrap(err))
			}
			defer response.Body.Close()
			awaitFontSignal(t, entered)
			grant := f.first.Load()
			if grant == nil || grant.Check() != nil {
				t.Fatal("serving grant closed before actual JSON body write")
			}
			if f.grants.Load() != 2 {
				t.Fatalf("grant count=%d; response and existing metadata check only", f.grants.Load())
			}
			switch mode {
			case "cancel":
				cancel()
			case "expire-during-write":
				f.clock.elapsed.Store(int64(2 * time.Minute))
				awaitFontSignal(t, grant.Context().Done())
				// Advancing the test clock wakes the watchdog. Wait for its actual
				// transport deadline before releasing the artificial slow write.
				awaitFontSignal(t, expired)
			}
			close(release)
			body, readErr := io.ReadAll(response.Body)
			awaitFontSignal(t, done)
			if grant.Check() == nil {
				t.Fatal("completed response leaked grant")
			}
			if mode == "slow-success" {
				var items []PlaybackSubtitleFont
				if readErr != nil || json.Unmarshal(body, &items) != nil || len(items) != 1 || items[0].Data != base64.StdEncoding.EncodeToString(font) {
					t.Fatalf("font output failed: bytes=%d err=%v", len(body), readErr)
				}
			} else if len(body) != 0 {
				t.Fatalf("canceled/expired output leaked %d body bytes", len(body))
			}
		})
	}
}
func TestSubtitleFontResponseHTTPExpiryBeforeEncoding(t *testing.T) {
	media, ffmpeg, _ := fontLifetimeMedia(t)
	f := newFontLifetimeFixture(t, media, ffmpeg, "", func(f *fontLifetimeFixture) {
		f.clock.elapsed.Store(int64(2 * time.Minute))
		awaitFontSignal(t, f.first.Load().Context().Done())
	})
	server := httptest.NewServer(f.handler)
	defer server.Close()
	response, err := server.Client().Do(fontRequest(t, t.Context(), server.URL+f.path))
	if err == nil {
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if len(body) != 0 {
			t.Fatalf("expired prepared bundle leaked %d bytes", len(body))
		}
	}
	if grant := f.first.Load(); grant == nil || grant.Check() == nil {
		t.Fatal("expiry did not close serving authority")
	}
}
func TestSubtitleFontResponseHTTPAdmission(t *testing.T) {
	media, ffmpeg, _ := fontLifetimeMedia(t)
	for _, tc := range []struct {
		mode   string
		status int
		grants int64
	}{
		{"encoded-api", 200, 2}, {"proxy", 503, 0}, {"remote-api", 503, 0}, {"bad-signature", 503, 0}, {"expired-reference", 503, 0}, {"foreign-account", 403, 2}, {"foreign-profile", 403, 2},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			f := newFontLifetimeFixture(t, media, ffmpeg, tc.mode, nil)
			server := httptest.NewServer(f.handler)
			defer server.Close()
			response, err := server.Client().Do(fontRequest(t, t.Context(), server.URL+f.path))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status || f.grants.Load() != tc.grants {
				t.Fatalf("status=%d grants=%d body=%s", response.StatusCode, f.grants.Load(), body)
			}
			if tc.status != 200 && bytes.Contains(body, []byte("synthetic.ttf")) {
				t.Fatal("refusal exposed fonts")
			}
		})
	}
}
