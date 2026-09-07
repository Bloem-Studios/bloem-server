package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// boundSubtitleFixture is nativeBoundDirectFixture with an external SRT track
// on the recipe's media file. The serving grant is real (AcquireRuntimeGrantV3
// under a test clock) and counts how many grants were issued.
func boundSubtitleFixture(t *testing.T) (*StreamHandler, *playback.RecipeCard, string, *int) {
	t.Helper()
	namespace := &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
	card := playback.NewDirectRecipeCard("logical", 1, "profile", 42)
	card.Executor, card.TranscodeTransportID = namespace, "transport"
	card.RoutingWorkload, card.RoutingExecution, card.RoutingEgress = string(noderouting.WorkloadDirectPlay), string(noderouting.ExecutionNone), string(noderouting.EgressAPI)
	sessions := playback.NewSessionManager(0, 0)
	sessions.RegisterReconstructed(&playback.Session{ID: card.SessionID, UserID: 1, ProfileID: "profile", MediaFileID: 42, PlayMethod: playback.PlayDirect, Executor: namespace, TranscodeTransportID: card.TranscodeTransportID,
		RoutingWorkload: card.RoutingWorkload, RoutingExecution: card.RoutingExecution, RoutingEgress: card.RoutingEgress})
	dir := t.TempDir()
	media := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(media, bytes.Repeat([]byte("x"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	srt := filepath.Join(dir, "video.eng.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nsynthetic cue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ass := filepath.Join(dir, "video.jpn.ass")
	//nolint:misspell // "Dialogue" is the ASS event keyword
	if err := os.WriteFile(ass, []byte("[Script Info]\nScriptType: v4.00+\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,styled cue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := &models.MediaFile{ID: 42, FilePath: media, ExternalSubtitles: []models.ExternalSubtitle{{Path: srt, Language: "eng", Format: "srt"}, {Path: ass, Language: "jpn", Format: "ass"}}}
	h := NewStreamHandler(sessions, testPlaybackFileResolver{file: file})
	h.JWTSecret = "test-secret"
	h.TM.Sessions = sessions
	h.TM.ResolveExecutorRecipe = func(_ context.Context, transport string, ns playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
		if transport != card.TranscodeTransportID || ns != *namespace {
			return nil, errors.New("wrong immutable recipe binding")
		}
		return &card, nil
	}
	grants := new(int)
	clock := new(nativeGrantClock)
	h.TM.ExecuteGrants = func(ctx context.Context, transport string, ns playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		if transport != card.TranscodeTransportID || ns != *namespace || purpose != playback.AttemptGrantServeV3 {
			return nil, errors.New("wrong serve binding")
		}
		*grants++
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
	return h, &card, token, grants
}

type testSubtitleFontLifetime struct {
	http.ResponseWriter
	cleanup func()
	output  http.ResponseWriter
}

func (w *testSubtitleFontLifetime) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *testSubtitleFontLifetime) RetainSubtitleFontResponse(guarded http.ResponseWriter, _ *http.Request, cleanup func()) {
	w.output, w.cleanup = guarded, cleanup
}

func boundSubtitleRouter(h *StreamHandler) http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := apimw.SetClaims(r.Context(), &auth.Claims{UserID: 1, Role: "user", TokenType: auth.TokenTypeAccess})
			next.ServeHTTP(w, r.WithContext(apimw.SetProfileID(ctx, "profile")))
		})
	})
	router.Handle("/stream/{session_id}/subtitles/{track}", h.InitialSubtitleDelivery(h.HandleInitialSubtitle))
	router.HandleFunc("/stream/{session_id}/subtitles/{track}/fonts", func(w http.ResponseWriter, r *http.Request) {
		lifetime := &testSubtitleFontLifetime{ResponseWriter: w}
		defer func() {
			if lifetime.cleanup != nil {
				lifetime.cleanup()
			}
		}()
		items, err := h.BoundSubtitleFontBundle(lifetime, r)
		if lifetime.output != nil {
			w = lifetime.output
		}
		if err != nil {
			writePlaybackOperationErrorEnvelope(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	})
	return router
}

// A bound session's external text track is served through the signed reference
// under one serving grant per response; HEAD carries the representation only;
// the unbound legacy handler still refuses the very same session.
func TestInitialSubtitleServesBoundExternalTrack(t *testing.T) {
	h, _, token, grants := boundSubtitleFixture(t)
	router := boundSubtitleRouter(h)
	get := httptest.NewRecorder()
	router.ServeHTTP(&nativeGrantRecorder{get}, httptest.NewRequest(http.MethodGet, "/stream/logical/subtitles/0.vtt?file_id=42&st="+token, nil))
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte("WEBVTT")) || !bytes.Contains(get.Body.Bytes(), []byte("synthetic cue")) || get.Header().Get("Content-Type") != "text/vtt; charset=utf-8" {
		t.Fatalf("GET = %d %q %v", get.Code, get.Body.String(), get.Header())
	}
	head := httptest.NewRecorder()
	router.ServeHTTP(&nativeGrantRecorder{head}, httptest.NewRequest(http.MethodHead, "/stream/logical/subtitles/0.vtt?file_id=42&st="+token, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Type") != "text/vtt; charset=utf-8" {
		t.Fatalf("HEAD = %d %q %v", head.Code, head.Body.String(), head.Header())
	}
	// Two serving grants per admitted response, exactly as the media bytes take
	// them: one held for the response by the executor guard and one consumed by
	// the session's metadata check.
	if *grants != 4 {
		t.Fatalf("serving grants = %d, want two per response", *grants)
	}
	legacy := httptest.NewRecorder()
	h.HandleSubtitle(legacy, playbackTestRequest(http.MethodGet, "/api/v1/stream/logical/subtitles/0.vtt?st="+token, nil, map[string]string{"session_id": "logical", "track": "0.vtt"}))
	if legacy.Code != http.StatusServiceUnavailable {
		t.Fatalf("legacy handler admitted a bound session: %d", legacy.Code)
	}
}

func TestInitialSubtitlePinnedIdentitySurvivesInventoryChanges(t *testing.T) {
	h, _, token, grants := boundSubtitleFixture(t)
	file, err := h.fileResolver.GetByID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	selected := file.ExternalSubtitles[0]
	file.ExternalSubtitles[0], file.ExternalSubtitles[1] = file.ExternalSubtitles[1], file.ExternalSubtitles[0]
	file.SubtitleTracks = []models.SubtitleTrack{{Index: 7, Codec: "ass"}}
	router := boundSubtitleRouter(h)
	for _, tc := range []struct {
		name, method, track, pin, contentType, body string
	}{
		{"external reordered", http.MethodGet, "0.vtt", "external_subtitle_key=" + playback.ExternalSubtitlePathKeyV3(selected.Path), "text/vtt; charset=utf-8", "synthetic cue"},
		{"embedded after sidecar insertion", http.MethodHead, "0.ass", "embedded_stream_index=7", "text/x-ssa; charset=utf-8", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(&nativeGrantRecorder{rec}, httptest.NewRequest(tc.method, "/stream/logical/subtitles/"+tc.track+"?file_id=42&st="+url.QueryEscape(token)+"&"+tc.pin, nil))
			if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != tc.contentType || !bytes.Contains(rec.Body.Bytes(), []byte(tc.body)) {
				t.Fatalf("response = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
			}
			if tc.method == http.MethodHead && rec.Body.Len() != 0 {
				t.Fatal("HEAD returned a body")
			}
		})
	}
	if *grants != 4 {
		t.Fatalf("serving grants = %d, want two per response", *grants)
	}
}

func TestInitialSubtitleAndFontsRefuseInvalidOrMissingPins(t *testing.T) {
	for _, route := range []string{"0.vtt", "0/fonts"} {
		for _, tc := range []struct {
			name, query string
			status      int
		}{
			{"missing embedded", "embedded_stream_index=99", http.StatusNotFound},
			{"missing external", "external_subtitle_key=" + playback.ExternalSubtitlePathKeyV3("missing.srt"), http.StatusNotFound},
			{"invalid external", "external_subtitle_key=invalid", http.StatusBadRequest},
			{"empty pin", "embedded_stream_index=", http.StatusBadRequest},
			{"duplicate pin", "embedded_stream_index=7&embedded_stream_index=8", http.StatusBadRequest},
			{"conflicting pins", "embedded_stream_index=7&downloaded_subtitle_id=1", http.StatusBadRequest},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				h, _, token, grants := boundSubtitleFixture(t)
				rec := httptest.NewRecorder()
				boundSubtitleRouter(h).ServeHTTP(&nativeGrantRecorder{rec}, httptest.NewRequest(http.MethodGet, "/stream/logical/subtitles/"+route+"?file_id=42&st="+url.QueryEscape(token)+"&"+tc.query, nil))
				var envelope errorResponse
				if rec.Code != tc.status || json.Unmarshal(rec.Body.Bytes(), &envelope) != nil {
					t.Fatalf("response = %d %q, want %d", rec.Code, rec.Body.String(), tc.status)
				}
				wantCode := "not_found"
				if tc.status == http.StatusBadRequest {
					wantCode = "bad_request"
				}
				if envelope.Error != wantCode || *grants != 2 {
					t.Fatalf("refusal = %+v, serving grants = %d", envelope, *grants)
				}
			})
		}
	}
}

// Every admission failure is refused before any file or grant is touched.
func TestInitialSubtitleRefusesUnboundOrForeignRequests(t *testing.T) {
	h, card, token, grants := boundSubtitleFixture(t)
	legacyCard := playback.NewDirectRecipeCard("logical", 1, "profile", 42)
	legacyToken, err := streamtoken.Sign(legacyCard.ToClaims(), h.JWTSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	otherCard := *card
	otherCard.SessionID = "other"
	otherToken, err := streamtoken.Sign(otherCard.ToClaims(), h.JWTSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	router := boundSubtitleRouter(h)
	for name, tc := range map[string]struct {
		target string
		want   int
	}{
		"no signed reference":         {"/stream/logical/subtitles/0.vtt?file_id=42", http.StatusServiceUnavailable},
		"unbound legacy reference":    {"/stream/logical/subtitles/0.vtt?file_id=42&st=" + legacyToken, http.StatusServiceUnavailable},
		"reference for other session": {"/stream/logical/subtitles/0.vtt?file_id=42&st=" + otherToken, http.StatusServiceUnavailable},
		"foreign source file":         {"/stream/logical/subtitles/0.vtt?file_id=43&st=" + token, http.StatusBadRequest},
		"unknown track":               {"/stream/logical/subtitles/9.vtt?file_id=42&st=" + token, http.StatusNotFound},
		"fonts on a non-ASS track":    {"/stream/logical/subtitles/0/fonts?file_id=42&st=" + token, http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(&nativeGrantRecorder{rec}, httptest.NewRequest(http.MethodGet, tc.target, nil))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	// The three refusals before the guard issued no grant; the three that
	// reached the guard each took its serving grant plus the session metadata
	// check's grant.
	if *grants != 6 {
		t.Fatalf("grants issued = %d, want 6", *grants)
	}
	unconfigured := &StreamHandler{}
	rec := httptest.NewRecorder()
	unconfigured.InitialSubtitleDelivery(func(http.ResponseWriter, *http.Request) { t.Fatal("unconfigured producer reached delivery") }).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/logical/subtitles/0.vtt?st="+token, nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured = %d", rec.Code)
	}
}

// Bound plan URLs carry the API-local path and the same signed reference as
// the media bytes; burn-in-only tracks keep no URL.
func TestBindInitialSubtitleURLsV3(t *testing.T) {
	plan := &playback.PlanV3{Subtitle: playback.SubtitleDecisionV3{Mode: playback.SubtitleRenderV3, Artifact: &playback.SubtitleArtifactV3{URL: "/stream/s/subtitles/1.ass?file_id=42"}, Inventory: []playback.SubtitleInventoryItemV3{
		{CombinedIndex: 0, URL: "/stream/s/subtitles/0.vtt?file_id=42"},
		{CombinedIndex: 1, URL: "/stream/s/subtitles/1.ass?file_id=42", FontBundleURL: "/stream/s/subtitles/1/fonts?file_id=42"},
		{CombinedIndex: 2},
	}}}
	bindInitialSubtitleURLsV3(plan, "tok+en")
	if plan.Subtitle.Inventory[0].URL != "/api/v1/stream/s/subtitles/0.vtt?file_id=42&st=tok%2Ben" || plan.Subtitle.Inventory[1].FontBundleURL != "/api/v1/stream/s/subtitles/1/fonts?file_id=42&st=tok%2Ben" || plan.Subtitle.Inventory[2].URL != "" || plan.Subtitle.Artifact.URL != "/api/v1/stream/s/subtitles/1.ass?file_id=42&st=tok%2Ben" {
		t.Fatalf("bound URLs: %+v %+v", plan.Subtitle.Inventory, plan.Subtitle.Artifact)
	}
	bindInitialSubtitleURLsV3(nil, "x")
	plan.Subtitle.Inventory[0].URL = "https://proxy.example.test/keep"
	bindInitialSubtitleURLsV3(plan, "x")
	if plan.Subtitle.Inventory[0].URL != "https://proxy.example.test/keep" {
		t.Fatal("absolute URL rewritten")
	}
}

func writePlaybackOperationErrorEnvelope(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		writeError(w, apiErr.Status, apiErr.Code, apiErr.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

// An embedded ASS track of a bound session streams through extraction under the
// serving grant, and its attached fonts come back as the JSON bundle. Requires
// ffmpeg/ffprobe with libx264, matroska and ass support.
func TestInitialSubtitleServesBoundEmbeddedASSAndFonts(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required")
	}
	fontPath := "/System/Library/Fonts/Supplemental/Arial Bold.ttf"
	if _, err := os.Stat(fontPath); err != nil {
		t.Skip("system TTF fixture unavailable")
	}
	dir := t.TempDir()
	assPath := filepath.Join(dir, "sub.ass")
	//nolint:misspell // "Dialogue" is the ASS event keyword
	if err := os.WriteFile(assPath, []byte("[Script Info]\nScriptType: v4.00+\nPlayResX: 640\nPlayResY: 360\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,styled cue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(dir, "fixture.mkv")
	output, err := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=64x36:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-i", assPath, "-attach", fontPath, "-metadata:s:t:0", "mimetype=font/ttf", "-metadata:s:t:0", "filename=ArialBold.ttf", "-t", "1", "-map", "0:v", "-map", "1:a", "-map", "2:s", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-c:s", "ass", "-y", mediaPath).CombinedOutput()
	if err != nil {
		t.Skipf("fixture mkv: %v %s", err, output)
	}
	h, _, token, _ := boundSubtitleFixture(t)
	h.fileResolver = testPlaybackFileResolver{file: &models.MediaFile{ID: 42, FilePath: mediaPath, Container: "mkv", SubtitleTracks: []models.SubtitleTrack{{Index: 2, Codec: "ass", Language: "jpn"}}}}
	h.PlaybackConfig = func() config.PlaybackConfig { return config.PlaybackConfig{FFmpegPath: ffmpeg} }
	router := boundSubtitleRouter(h)
	get := httptest.NewRecorder()
	router.ServeHTTP(&nativeGrantRecorder{get}, httptest.NewRequest(http.MethodGet, "/stream/logical/subtitles/0.ass?file_id=42&st="+token, nil))
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte("styled cue")) || get.Header().Get("Content-Type") != "text/x-ssa; charset=utf-8" {
		t.Fatalf("embedded ASS: %d %q %v", get.Code, get.Body.String(), get.Header())
	}
	fonts := httptest.NewRecorder()
	router.ServeHTTP(&nativeGrantRecorder{fonts}, httptest.NewRequest(http.MethodGet, "/stream/logical/subtitles/0/fonts?file_id=42&st="+token, nil))
	var bundle []playback.SubtitleFontBundleItem
	if fonts.Code != http.StatusOK || json.Unmarshal(fonts.Body.Bytes(), &bundle) != nil || len(bundle) != 1 || bundle[0].Name != "ArialBold.ttf" || bundle[0].Data == "" {
		t.Fatalf("fonts: %d %q", fonts.Code, fonts.Body.String())
	}
	// The URL was published when the embedded track had combined ordinal 0.
	// A newly discovered external track must not redirect it or hide its fonts.
	file, err := h.fileResolver.GetByID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	sidecarPath := filepath.Join(dir, "discovered.ass")
	sidecar, err := os.ReadFile(assPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecarPath, bytes.ReplaceAll(sidecar, []byte("styled cue"), []byte("different sidecar cue")), 0o600); err != nil {
		t.Fatal(err)
	}
	file.ExternalSubtitles = []models.ExternalSubtitle{{Path: sidecarPath, Format: "ass"}}
	for _, route := range []string{"0.ass", "0/fonts"} {
		t.Run("pinned after sidecar insertion/"+route, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(&nativeGrantRecorder{rec}, httptest.NewRequest(http.MethodGet, "/stream/logical/subtitles/"+route+"?file_id=42&embedded_stream_index=2&st="+url.QueryEscape(token), nil))
			want := get.Body.Bytes()
			if route == "0/fonts" {
				want = fonts.Body.Bytes()
			}
			if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), want) {
				t.Fatalf("pinned response changed: %d %q", rec.Code, rec.Body.String())
			}
		})
	}
	legacy := httptest.NewRecorder()
	h.HandleSubtitleFonts(legacy, playbackTestRequest(http.MethodGet, "/api/v1/stream/logical/subtitles/0/fonts?st="+token, nil, map[string]string{"session_id": "logical", "track": "0"}))
	if legacy.Code != http.StatusServiceUnavailable {
		t.Fatalf("legacy fonts handler admitted a bound session: %d", legacy.Code)
	}
}
