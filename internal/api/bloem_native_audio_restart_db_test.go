//go:build integration

package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/playback/planstore"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// nativeAudioProcess is one server process: its own in-memory sessions over
// the durable attempt store and Redis every process shares.
type nativeAudioProcess struct {
	playback *handlers.PlaybackHandler
	stream   *handlers.StreamHandler
	sessions *playback.SessionManager
}

// process wires a server process the way the router does: the stream handler
// shares the playback handler's reconstruct manager, attempt store and session
// recipe store, and native recovery is installed on the manager.
func (f *nativeAudioAPIFixture) process(t *testing.T, attempts playback.PlanStoreV3) nativeAudioProcess {
	t.Helper()
	h, stream, sessions := f.handlers(t)
	h.PlanStoreV3 = attempts
	h.SessionRecipeStore = noderecipe.NewSessionRecipeStore(f.deps.RedisClient, 0)
	stream.TM = h.TranscodeManager()
	stream.PlanStoreV3 = h.PlanStoreV3
	stream.SessionRecipeStore = h.SessionRecipeStore
	h.InstallNativeVideoRecovery()
	return nativeAudioProcess{playback: h, stream: stream, sessions: sessions}
}

func (f *nativeAudioAPIFixture) startRestartable(t *testing.T, h *handlers.PlaybackHandler, headerAuth bool) playback.DecisionResponseV3 {
	t.Helper()
	position := 0.0
	features := []string{playback.FeaturePlaybackPlanV3}
	if headerAuth {
		features = append(features, playback.FeatureHeaderAuthenticatedMediaV3)
	}
	request := playback.StartRequestV3{ProtocolVersion: playback.ProtocolV3, ClientFeatures: features, FileID: f.files[0].ID, ProfileID: f.profile, PlaybackAttemptID: uuid.NewString(), QualityPreference: "auto", SubtitleFidelityPreference: playback.SubtitleFidelityCompatibleV3, StartPosition: &position, Capabilities: playback.ClientCodecCapabilitiesV3{VideoEvidence: playback.EvidenceExactV3, AudioEvidence: playback.EvidenceExactV3, CodecsVideo: []string{"h264"}, CodecsAudio: []string{"mp3"}, Containers: []string{"mp3"}}, ClientPlaybackContext: playback.ClientPlaybackContextV3{ProtocolVersion: playback.ProtocolV3, FormFactor: "mobile", AppVersion: "test", Device: playback.DeviceContextV3{Platform: "ios"}, Output: playback.OutputContextV3{OutputContextID: "audio-restart-fixture"}, Deliveries: map[string]playback.DeliveryCapabilityV3{playback.DeliveryClassOriginalHTTPV3: {Enabled: true, SupportedOnDevice: true}}}}
	response, err := h.StartPlaybackV2(f.ctx, f.playbackCaller(h), request)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if response.PlaybackPlan == nil || response.PlaybackPlan.Delivery != playback.DeliveryOriginalHTTPV3 || strings.Contains(response.PlaybackPlan.Stream.URL, "st=") == headerAuth {
		t.Fatalf("start plan=%+v", response.PlaybackPlan)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		keys := []string{playback.StreamDenyKey(response.SessionID), "bloem:native-video-session:" + response.SessionID, noderecipe.SessionRecipeKeyPrefix + response.SessionID}
		for _, file := range f.files {
			keys = append(keys, "bloem:native-video:"+response.SessionID+":"+strconv.Itoa(file.ID))
		}
		if err := f.deps.RedisClient.Del(ctx, keys...).Err(); err != nil {
			t.Errorf("session fixture cleanup: %v", err)
		}
	})
	return response
}

// tokenStream requests the stream the way a native player following a stream
// token does: the token is the only credential, so the request carries no
// bearer claims and no tenant, and viewer scope is resolved from the token's
// own uid/pid.
func (f *nativeAudioAPIFixture) tokenStream(t *testing.T, h *handlers.StreamHandler, started playback.DecisionResponseV3) *httptest.ResponseRecorder {
	t.Helper()
	uid := middleware.GetUserID(f.ctx)
	policy := nativeStorageActorPolicy(f.deps)
	tenant, err := policy.tenants.ResolveSubjectTenant(t.Context(), uid, f.profile)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := policy.viewer.ResolveCurrent(tenancy.WithContext(t.Context(), tenant), access.ResolveInput{UserID: uid, ProfileID: f.profile, SkipPINVerification: true})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v2"+started.PlaybackPlan.Stream.URL, nil).WithContext(t.Context())
	r.Header.Set("Range", "bytes=2-31")
	w := httptest.NewRecorder()
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !middleware.IsStreamTokenAuthorized(r.Context()) {
			t.Error("stream token did not authorize the request")
			return
		}
		ctx := middleware.SetProfileID(access.SetScope(r.Context(), scope), f.profile)
		route := chi.NewRouteContext()
		route.URLParams.Add("session_id", started.SessionID)
		ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
		defer func() {
			if value := recover(); value != nil {
				if err, ok := value.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
					panic(value)
				}
			}
		}()
		h.HandleStream(w, r.WithContext(ctx))
	})
	captureNativeVideoRequest((&middleware.AuthMiddleware{}).StreamTokenAuth(f.deps.Config.Auth.JWTSecret)(serve)).ServeHTTP(w, r)
	return w
}

// A server restart loses the in-memory playback session; the durable attempt,
// the native binding and either the server-side session recipe
// (header-authenticated media) or the stream token remain. The player's next
// range request must resume the native audiobook under the same authority
// checks, and a stopped attempt must stay stopped.
func TestNativeAudioStreamSurvivesRestartDB(t *testing.T) {
	for _, mode := range []string{"header-authenticated", "stream-token"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeAudioAPIFixture(t)
			attempts := planstore.NewPostgres(f.pool)
			headerAuth := mode == "header-authenticated"
			get := func(p nativeAudioProcess, started playback.DecisionResponseV3) *httptest.ResponseRecorder {
				if headerAuth {
					return f.stream(t, p.stream, started, http.MethodGet)
				}
				return f.tokenStream(t, p.stream, started)
			}
			before := f.process(t, attempts)
			started := f.startRestartable(t, before.playback, headerAuth)
			if w := get(before, started); w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), f.data[2:32]) {
				t.Fatalf("before restart=%d %s", w.Code, w.Body.String())
			}
			if headerAuth {
				// Recovery runs the native source authority check only for a
				// recipe that names the native file.
				card, ok := before.playback.SessionRecipeStore.Get(t.Context(), started.SessionID)
				if !ok || card == nil || card.InputPath != f.files[0].FilePath || !card.RequireMediaAuthorization {
					t.Fatalf("session recipe=%+v ok=%v", card, ok)
				}
			}

			after := f.process(t, attempts)
			if _, err := after.sessions.GetSession(started.SessionID); !errors.Is(err, playback.ErrSessionNotFound) {
				t.Fatalf("restart kept the session: %v", err)
			}
			if w := get(after, started); w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), f.data[2:32]) {
				t.Fatalf("after restart=%d %s", w.Code, w.Body.String())
			}
			if _, err := after.sessions.GetSession(started.SessionID); err != nil {
				t.Fatalf("restart did not recover the session: %v", err)
			}

			if _, err := after.playback.StopPlaybackV2(f.ctx, f.playbackCaller(after.playback), started.SessionID, handlers.PlaybackStopCommand{StopID: uuid.NewString()}); err != nil {
				t.Fatalf("stop: %v", err)
			}
			again := f.process(t, attempts)
			if w := get(again, started); w.Code < 400 || bytes.Contains(w.Body.Bytes(), f.data[2:32]) {
				t.Fatalf("stopped session after restart=%d", w.Code)
			}
		})
	}
}
