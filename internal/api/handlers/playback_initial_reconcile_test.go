package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func initialReconciliationBinding(t *testing.T, f *initialHTTPFixture) playback.InitialActivationBindingV3 {
	t.Helper()
	ctx := t.Context()
	reserved, err := f.flow.Control.ReserveAttempt(ctx, playback.AttemptReservationRequestV3{PlaybackAttemptID: f.request.PlaybackAttemptID, UserID: f.userID, ProfileID: f.request.ProfileID, RequestedMediaFileID: f.request.FileID, RequestDigest: "reconciliation-fixture", NormalizedRequest: f.request, OwnerID: f.flow.OwnerID, LeaseDuration: time.Minute, Retention: time.Hour})
	if err != nil || !reserved.Owned {
		t.Fatalf("reserve: %+v %v", reserved, err)
	}
	binding := playback.InitialActivationBindingV3{Source: f.source.Source(), Scope: userstore.PlaybackProgressScope{ProfileID: f.request.ProfileID, SessionID: uuid.NewString(), MediaItemID: f.itemID}, Fence: userstore.PlaybackProgressFence{AttemptID: f.request.PlaybackAttemptID, Incarnation: reserved.Authority.Incarnation, OwnerID: reserved.Authority.OwnerID, Epoch: reserved.Authority.Epoch}, IntentID: uuid.NewString(), Progress: userstore.PlaybackProgressSample{DurationSeconds: 1000}}
	if err := f.pool.QueryRow(ctx, `SELECT admission_id::text FROM playback_source_registrations WHERE user_id=$1`, f.userID).Scan(&binding.AdmissionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.flow.Control.BeginInitialActivation(ctx, binding); err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestInitialPlaybackReconcileExpiredIntent(t *testing.T) {
	for _, scenario := range []string{"pending", "installed", "existing terminal", "source unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInitialHTTPFixture(t)
			binding := initialReconciliationBinding(t, f)
			ctx := t.Context()
			var original userstore.PlaybackProgressState
			if scenario != "pending" && scenario != "source unavailable" {
				if _, err := f.source.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: binding.Scope, Next: binding.Fence}); err != nil {
					t.Fatal(err)
				}
				receipt, err := playback.ReadInitialActivationReceiptV3(ctx, binding, f.source)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.flow.Control.AcknowledgeInitialInstallation(ctx, binding, receipt); err != nil {
					t.Fatal(err)
				}
				if scenario == "existing terminal" {
					result, err := f.source.StopPlaybackProgress(ctx, userstore.StopPlaybackProgressRequest{Scope: binding.Scope, Fence: binding.Fence, StopID: uuid.NewString()})
					if err != nil {
						t.Fatal(err)
					}
					original = result.State
				}
			}
			if scenario == "source unavailable" {
				if _, err := f.pool.Exec(ctx, `UPDATE playback_source_markers SET gate='sealed' WHERE user_id=$1`, f.userID); err != nil {
					t.Fatal(err)
				}
			}
			// The retained intent remains eligible even after both original time bounds.
			if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_lease_expires_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE playback_attempt_id=$1`, f.request.PlaybackAttemptID); err != nil {
				t.Fatal(err)
			}
			result, err := f.handler.ReconcileInitialPlayback(ctx, f.userID, "", 10)
			if scenario == "source unavailable" {
				if err == nil || result.Pending != 1 || result.Completed != 0 {
					t.Fatalf("unavailable source: %+v %v", result, err)
				}
				state, err := f.flow.Control.ReadInitialActivation(ctx, binding)
				if err != nil || state.Phase != playback.InitialActivationAbortingV3 || state.Terminal != nil {
					t.Fatalf("intent not retained: %+v %v", state, err)
				}
				return
			}
			if err != nil || result.Completed != 1 || result.Pending != 0 {
				t.Fatalf("reconcile: %+v %v", result, err)
			}
			state, err := f.flow.Control.ReadInitialActivation(ctx, binding)
			if err != nil || state.Phase != playback.InitialActivationAbortedV3 || state.Terminal == nil {
				t.Fatalf("control terminal: %+v %v", state, err)
			}
			source, err := f.source.ReadPlaybackProgress(ctx, binding.Scope)
			if err != nil || source.Stop == nil || source.Last != nil || source.Stop.History != nil {
				t.Fatalf("source terminal: %+v %v", source, err)
			}
			if scenario == "existing terminal" && (!reflect.DeepEqual(source, original) || state.AbortID == source.Stop.StopID) {
				t.Fatal("reconciliation replaced existing terminal receipt")
			}
		})
	}
}

func TestInitialPlaybackReconcileNormalStopRequiresReceipt(t *testing.T) {
	for _, hasReceipt := range []bool{false, true} {
		name := "without receipt"
		if hasReceipt {
			name = "committed receipt"
		}
		t.Run(name, func(t *testing.T) {
			f := newInitialHTTPFixture(t)
			ctx := t.Context()
			status, data := f.call(t, http.MethodPost, "/start", f.request)
			if status != 201 {
				t.Fatalf("start: %d %s", status, data)
			}
			var decision playback.DecisionResponseV3
			if err := json.Unmarshal(data, &decision); err != nil {
				t.Fatal(err)
			}
			active, err := f.flow.Control.GetActivatedPlaybackAuthority(ctx, f.userID, f.request.ProfileID, decision.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			stopID := uuid.NewString()
			if _, err := f.flow.Control.BeginBoundStop(ctx, active.Binding, stopID); err != nil {
				t.Fatal(err)
			}
			before, err := f.source.ReadPlaybackProgress(ctx, active.Binding.Scope)
			if err != nil {
				t.Fatal(err)
			}
			if hasReceipt {
				sample := active.Binding.Progress
				sample.Sequence = 7
				sample.PositionSeconds = 200
				result, err := f.source.StopPlaybackProgress(ctx, userstore.StopPlaybackProgressRequest{Scope: active.Binding.Scope, Fence: active.Binding.Fence, StopID: stopID, FinalSample: &sample})
				if err != nil {
					t.Fatal(err)
				}
				before = result.State
			}
			result, err := f.handler.ReconcileInitialPlayback(ctx, f.userID, "", 10)
			after, readErr := f.source.ReadPlaybackProgress(ctx, active.Binding.Scope)
			if readErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("reconciler mutated source: before=%+v after=%+v err=%v", before, after, readErr)
			}
			state, readErr := f.flow.Control.ReadInitialActivation(ctx, active.Binding)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if hasReceipt {
				if err != nil || result.Completed != 1 || state.Phase != playback.InitialActivationStoppedV3 {
					t.Fatalf("receipt completion: %+v %+v %v", result, state, err)
				}
			} else {
				if err == nil || result.Pending != 1 || state.Phase != playback.InitialActivationStoppingV3 || state.Terminal != nil {
					t.Fatalf("missing receipt synthesized: %+v %+v %v", result, state, err)
				}
			}
		})
	}
}

func TestInitialPlaybackReconcileClosesMatchingTranscode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("local runtime cleanup requires ffmpeg")
	}
	f := newInitialHTTPFixture(t)
	ctx := t.Context()
	command := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "12", "-c:v", "mpeg4", "-c:a", "aac", f.file.FilePath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("synthetic media: %v %s", err, output)
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
	if status != 201 {
		t.Fatalf("start: %d %s", status, data)
	}
	var decision playback.DecisionResponseV3
	if err := json.Unmarshal(data, &decision); err != nil {
		t.Fatal(err)
	}
	runtime := f.handler.TranscodeManager().GetTranscodeSession(decision.SessionID)
	if runtime == nil || runtime.ExecutorNamespace() == nil {
		t.Fatal("missing bound runtime")
	}
	t.Cleanup(func() { _ = runtime.Close() })
	outputDir, err := runtime.ExecutorNamespace().OutputDir(root)
	if err != nil {
		t.Fatal(err)
	}
	active, err := f.flow.Control.GetActivatedPlaybackAuthority(ctx, f.userID, f.request.ProfileID, decision.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	stopID := uuid.NewString()
	if _, err := f.flow.Control.BeginBoundStop(ctx, active.Binding, stopID); err != nil {
		t.Fatal(err)
	}
	sample := active.Binding.Progress
	sample.Sequence = 1
	sample.PositionSeconds = 3
	stopped, err := f.source.StopPlaybackProgress(ctx, userstore.StopPlaybackProgressRequest{Scope: active.Binding.Scope, Fence: active.Binding.Fence, StopID: stopID, FinalSample: &sample})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		result, err := f.handler.ReconcileInitialPlayback(ctx, f.userID, "", 10)
		if err == nil && result.Completed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("drain did not complete: %+v %v", result, err)
		}
	}
	if f.handler.TranscodeManager().GetTranscodeSession(decision.SessionID) != nil {
		t.Fatal("reconciled runtime remains registered")
	}
	if _, err := os.Stat(outputDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reconciled executor output remains: %v", err)
	}
	after, err := f.source.ReadPlaybackProgress(ctx, active.Binding.Scope)
	if err != nil || !reflect.DeepEqual(after, stopped.State) {
		t.Fatalf("cleanup rewrote terminal receipt: %+v %v", after, err)
	}
}

func TestInitialPlaybackTerminalCleanupPreservesOtherBinding(t *testing.T) {
	h := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	h.initialFlow = &InitialPlaybackFlowV3{}
	binding := playback.InitialActivationBindingV3{Scope: userstore.PlaybackProgressScope{SessionID: "logical"}, Fence: userstore.PlaybackProgressFence{AttemptID: "attempt", Epoch: 1}}
	retained := &initialPendingPublicationV3{binding: binding}
	h.initialFlow.pending.Store(binding.Scope.SessionID, retained)
	other := binding
	other.Fence.Epoch = 2
	h.closeInitialRuntimeV3(other)
	if value, ok := h.initialFlow.pending.Load(binding.Scope.SessionID); !ok || value != retained {
		t.Fatal("cleanup discarded unresolved different binding")
	}
	h.closeInitialRuntimeV3(binding)
	if _, ok := h.initialFlow.pending.Load(binding.Scope.SessionID); ok {
		t.Fatal("terminal binding remains retained")
	}
}

func TestInitialPlaybackCapabilitiesExcludeUnsupportedActions(t *testing.T) {
	features := initialServerFeaturesV3()
	if !slices.Contains(features, "sequenced_progress_v1") || !slices.Contains(features, playback.FeaturePlaybackPlanV3) {
		t.Fatal("required initial capabilities missing")
	}
	for _, unsupported := range []string{"seek_reanchor_v1", "output_change_v1", "playback_route_diagnostics"} {
		if slices.Contains(features, unsupported) {
			t.Fatalf("unsupported action advertised: %s", unsupported)
		}
	}
}
