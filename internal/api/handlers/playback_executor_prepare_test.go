package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/google/uuid"
)

func executorTransportTestCard() playback.RecipeCard {
	return playback.RecipeCard{Executor: &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()},
		SessionID: uuid.NewString(), TranscodeTransportID: uuid.NewString(), InputPath: "/fixtures/movie.mkv", PlayMethod: playback.PlayTranscode,
		RoutingWorkload: "video_transcode", RoutingExecution: "transcode", RoutingExecutionNodeID: 1, RoutingEgress: "api", HWAccel: playback.HWAccelNone,
		TargetCodecVideo: "h264", SegmentDuration: 2}
}

func TestRemoteExecutorPreparationSingleExchange(t *testing.T) {
	for _, outcome := range []string{"prepared", "foreign", "changed", "empty", "lost", "redirect"} {
		t.Run(outcome, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/transcode/prepare" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fixture-secret" {
					t.Errorf("unexpected prepare exchange %s %s", r.Method, r.URL.Path)
				}
				var body transcodenode.ExecutorPreparation
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				switch outcome {
				case "lost":
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				case "redirect":
					http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
					return
				case "foreign":
					body.Recipe.Executor = executorTransportTestCard().Executor
				case "changed":
					body.Recipe.TargetCodecVideo = "hevc"
				case "empty":
					return
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			card := executorTransportTestCard()
			card.TranscodeNodeURL = server.URL
			h := &PlaybackHandler{JWTSecret: "fixture-secret"}
			_, err := h.prepareRemoteExecutorRecipeV3(t.Context(), card)
			if (err == nil) != (outcome == "prepared") {
				t.Fatalf("outcome %s error %v", outcome, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("preparation repeated %d times", calls.Load())
			}
		})
	}
}

func TestBoundRemoteStartRequiresReceiptWithoutReplay(t *testing.T) {
	for _, outcome := range []string{"ready", "empty202", "foreign", "changed", "lost", "redirect"} {
		t.Run(outcome, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/transcode/start" || r.Method != http.MethodPost {
					t.Errorf("unexpected start/cleanup exchange %s %s", r.Method, r.URL.Path)
				}
				var request transcodenode.TranscodeStartRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if outcome == "lost" {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				}
				if outcome == "redirect" {
					http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
					return
				}
				w.WriteHeader(http.StatusAccepted)
				if outcome == "empty202" {
					return
				}
				response := transcodenode.TranscodeStartResponse{Executor: request.Executor, ExecutorRecipeDigest: request.ExecutorRecipeDigest, SessionID: request.SessionID, Status: "started", HWAccel: request.HWAccel}
				if outcome == "foreign" {
					response.Executor = executorTransportTestCard().Executor
				}
				if outcome == "changed" {
					response.ExecutorRecipeDigest += "wrong"
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			card := executorTransportTestCard()
			card.TranscodeNodeURL = server.URL
			request, err := transcodenode.BoundTranscodeStartRequest(card)
			if err != nil {
				t.Fatal(err)
			}
			h := &PlaybackHandler{JWTSecret: "fixture-secret"}
			_, status, err := h.startRemotePlaybackTransport(t.Context(), server.URL, request)
			accepted := err == nil && status == http.StatusAccepted
			if accepted != (outcome == "ready") {
				t.Fatalf("outcome %s status%d error%v", outcome, status, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("start repeated %d times", calls.Load())
			}
		})
	}
}
