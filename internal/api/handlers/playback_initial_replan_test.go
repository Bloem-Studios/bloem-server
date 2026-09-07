package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

func initialReplanRequestV3(decision playback.DecisionResponseV3, request playback.StartRequestV3, id string, position float64) playback.ReplanRequestV3 {
	plan := decision.PlaybackPlan
	return playback.ReplanRequestV3{ProtocolVersion: playback.ProtocolV3, Operation: playback.ReplanOperationSeekReanchorV3, PlaybackAttemptID: request.PlaybackAttemptID, ReplanRequestID: id, FailedPlanID: plan.PlanID, PlanAttemptID: "plan-attempt-0001", PlanAttemptKey: plan.PlanAttemptKey, AttemptedPlanKeys: []string{}, AttemptCount: 1, QualityPreference: request.QualityPreference, PositionSeconds: position, SelectedTracks: plan.SelectedTracks, Capabilities: request.Capabilities, ClientPlaybackContext: request.ClientPlaybackContext}
}

func replanCommandV3(t *testing.T, req playback.ReplanRequestV3) PlaybackReplanCommand {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return PlaybackReplanCommand{Request: req, Digest: ReplanDigestV3(data)}
}

// TestInitialPlaybackBoundReplanDirect proves a direct attempt replans under its
// owner lease: the durable plan advances, the replay is byte-identical, a
// reused request id with different input is refused, stale bases are refused,
// and the legacy replan writer still cannot touch the authority-owned row.
func TestInitialPlaybackBoundReplanDirect(t *testing.T) {
	f := newInitialHTTPFixture(t)
	ctx := initialContextV3(t, f)
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("start: %d %s", status, data)
	}
	var started playback.DecisionResponseV3
	if err := json.Unmarshal(data, &started); err != nil {
		t.Fatal(err)
	}
	sessionID := started.SessionID
	caller := initialCallerV3(f)
	first := initialReplanRequestV3(started, f.request, "replan-0001-direct", 42.5)
	replanned, err := f.handler.ReplanInitialPlayback(ctx, caller, sessionID, replanCommandV3(t, first))
	if err != nil {
		t.Fatalf("replan: %v", err)
	}
	plan := replanned.PlaybackPlan
	if replanned.SessionID != sessionID || plan == nil || plan.PlanID == started.PlaybackPlan.PlanID || plan.Timeline.SourceStartSeconds != 42.5 || plan.Timeline.PlayerStartSeconds != 42.5 || !plan.Timeline.CanSeekAnywhere || !strings.HasPrefix(plan.Stream.URL, "/api/v1/stream/"+sessionID+"?st=") {
		t.Fatalf("replanned plan: %+v", replanned)
	}
	for _, unsupported := range []string{playback.FeatureSeekReanchorV3, playback.FeatureOutputChangeV3, playback.FeatureRouteDiagnostics} {
		for _, feature := range replanned.ServerFeatures {
			if feature == unsupported {
				t.Fatalf("replan advertised unsupported feature %s", unsupported)
			}
		}
	}
	// The new stream URL is served by the committed successor generation.
	if status, body := f.call(t, http.MethodGet, plan.Stream.URL, nil); status != http.StatusOK || len(body) == 0 {
		t.Fatalf("replanned media: %d bytes=%d", status, len(body))
	}
	record, err := f.flow.Control.GetAttempt(ctx, sessionID)
	if err != nil || record.CurrentReplanRequestID != "replan-0001-direct" || record.CurrentPlan.Timeline.SourceStartSeconds != 42.5 || record.StartResponse.PlaybackPlan == nil || record.StartResponse.PlaybackPlan.Timeline.SourceStartSeconds != 42.5 {
		t.Fatalf("durable record: %+v %v", record, err)
	}
	if record.NormalizedRequest.StartPosition == nil || *record.NormalizedRequest.StartPosition != 42.5 {
		t.Fatalf("normalized request position: %+v", record.NormalizedRequest.StartPosition)
	}
	// An exact retry replays the committed decision without a second commit.
	replayed, err := f.handler.ReplanInitialPlayback(ctx, caller, sessionID, replanCommandV3(t, first))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	a, _ := json.Marshal(replanned)
	b, _ := json.Marshal(replayed)
	if !bytes.Equal(a, b) {
		t.Fatalf("replay differs:\n%s\n%s", a, b)
	}
	// The same request id with a different position is an idempotency violation.
	changed := first
	changed.PositionSeconds = 43
	_, err = f.handler.ReplanInitialPlayback(ctx, caller, sessionID, replanCommandV3(t, changed))
	requirePlaybackOperationError(t, err, http.StatusConflict, "idempotency_key_reused")
	// A second replan must name the current plan; the replaced decision carries
	// a fresh plan ID and the sequence continues from the last request ID.
	second := initialReplanRequestV3(replanned, f.request, "replan-0002-direct", 10)
	secondDecision, err := f.handler.ReplanInitialPlayback(ctx, caller, sessionID, replanCommandV3(t, second))
	if err != nil {
		t.Fatalf("second replan: %v", err)
	}
	record, err = f.flow.Control.GetAttempt(ctx, sessionID)
	if err != nil || record.CurrentReplanRequestID != "replan-0002-direct" || record.CurrentPlan.Timeline.SourceStartSeconds != 10 {
		t.Fatalf("second durable record: %+v %v", record, err)
	}
	// Replaying the superseded first request is a stale plan, not a silent replay.
	_, err = f.handler.ReplanInitialPlayback(ctx, caller, sessionID, replanCommandV3(t, first))
	requirePlaybackOperationError(t, err, http.StatusConflict, "stale_playback_plan")
	// The legacy writer cannot update an authority-owned row.
	if _, err := f.flow.Control.BeginReplan(ctx, sessionID, "legacy-0001", "digest", record.CurrentReplanRequestID, time.Now().Add(time.Minute)); !errors.Is(err, playback.ErrStaleAttemptAuthorityV3) {
		t.Fatalf("legacy replan writer admitted an owned row: %v", err)
	}
	// Another profile, a foreign installation and an unsupported operation are refused before any write.
	_, err = f.handler.ReplanInitialPlayback(ctx, PlaybackCaller{UserID: f.userID, ProfileID: uuid.NewString(), InstallationID: f.flow.InstallationID}, sessionID, replanCommandV3(t, second))
	requirePlaybackOperationError(t, err, http.StatusForbidden, "forbidden")
	_, err = f.handler.ReplanInitialPlayback(ctx, PlaybackCaller{UserID: f.userID, ProfileID: f.request.ProfileID, InstallationID: uuid.NewString()}, sessionID, replanCommandV3(t, second))
	requirePlaybackOperationError(t, err, http.StatusConflict, "installation_changed")
	quality := initialReplanRequestV3(secondDecision, f.request, "replan-0003-quality", 10)
	quality.Operation = playback.ReplanOperationQualityChangeV3
	quality.QualityPreference = "480p"
	_, err = f.handler.ReplanInitialPlayback(ctx, caller, sessionID, replanCommandV3(t, quality))
	requirePlaybackOperationError(t, err, http.StatusNotImplemented, "capability_unsupported")
	beyond := initialReplanRequestV3(secondDecision, f.request, "replan-0004-beyond", 5000)
	_, err = f.handler.ReplanInitialPlayback(ctx, caller, sessionID, replanCommandV3(t, beyond))
	requirePlaybackOperationError(t, err, http.StatusUnprocessableEntity, "invalid_seek_position")
	_, err = f.handler.ReplanInitialPlayback(ctx, caller, uuid.NewString(), replanCommandV3(t, second))
	requirePlaybackOperationError(t, err, http.StatusNotFound, "session_not_found")
	record, err = f.flow.Control.GetAttempt(ctx, sessionID)
	if err != nil || record.CurrentReplanRequestID != "replan-0002-direct" {
		t.Fatalf("refusals wrote: %+v %v", record, err)
	}
	// After a stop the row is no longer active; a replan is fenced out.
	stopID := uuid.NewString()
	if _, err := f.handler.StopInitialPlayback(ctx, caller, sessionID, PlaybackStopCommand{StopID: stopID}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	_, err = f.handler.ReplanInitialPlayback(ctx, caller, sessionID, replanCommandV3(t, initialReplanRequestV3(replanned, f.request, "replan-0005-after-stop", 1)))
	if err == nil {
		t.Fatal("replan after stop was admitted")
	}
}

// TestInitialPlaybackBoundReplanLostOwner proves that a lost owner lease fences
// the bound replan writer even when the row is still active in the store.
func TestInitialPlaybackBoundReplanLostOwner(t *testing.T) {
	f := newInitialHTTPFixture(t)
	ctx := initialContextV3(t, f)
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("start: %d %s", status, data)
	}
	var started playback.DecisionResponseV3
	if err := json.Unmarshal(data, &started); err != nil {
		t.Fatal(err)
	}
	// Advance the epoch behind the running owner: the fence no longer matches.
	if _, err := f.pool.Exec(ctx, `UPDATE playback_v3_attempts SET control_epoch = control_epoch + 1 WHERE playback_attempt_id = $1`, f.request.PlaybackAttemptID); err != nil {
		t.Fatal(err)
	}
	_, err := f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), started.SessionID, replanCommandV3(t, initialReplanRequestV3(started, f.request, "replan-lost-owner", 5)))
	requirePlaybackOperationError(t, err, http.StatusServiceUnavailable, "unavailable")
	var current string
	if err := f.pool.QueryRow(ctx, `SELECT current_replan_request_id FROM playback_v3_attempts WHERE playback_attempt_id = $1`, f.request.PlaybackAttemptID).Scan(&current); err != nil || current != "" {
		t.Fatalf("fenced replan wrote: %q %v", current, err)
	}
}

// A local encoded seek prepares a separate namespace and publishes it only
// after the predecessor grant barrier. Old media URLs cannot regain authority.
func TestInitialPlaybackBoundReplanLocalTranscode(t *testing.T) {
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
	var started playback.DecisionResponseV3
	if err := json.Unmarshal(data, &started); err != nil {
		t.Fatal(err)
	}
	if started.PlaybackPlan == nil || started.PlaybackPlan.Delivery != playback.DeliveryTranscodeHLSV3 {
		t.Fatalf("expected encoded HLS: %s", data)
	}
	sessionID := started.SessionID
	before := f.handler.TranscodeManager().GetTranscodeSession(sessionID)
	if before == nil {
		t.Fatal("missing authority-bound transcode runtime")
	}
	ctx := initialContextV3(t, f)
	replanned, err := f.handler.ReplanInitialPlayback(ctx, initialCallerV3(f), sessionID, replanCommandV3(t, initialReplanRequestV3(started, f.request, "replan-0001-hls", 6)))
	if err != nil {
		t.Fatal(err)
	}
	after := f.handler.TranscodeManager().GetTranscodeSession(sessionID)
	if after == nil || after == before || *after.Opts().Executor == *before.Opts().Executor {
		t.Fatal("encoded successor reused predecessor runtime")
	}
	if replanned.PlaybackPlan == nil || replanned.PlaybackPlan.PlanID == started.PlaybackPlan.PlanID {
		t.Fatal("replacement plan missing")
	}

	record, err := f.flow.Control.GetAttempt(ctx, sessionID)
	if err != nil || record.CurrentReplanRequestID != "replan-0001-hls" || record.CurrentPlan.Timeline.SourceStartSeconds != 6 {
		t.Fatalf("successor record missing: %+v %v", record, err)
	}
	if status, _ = f.call(t, http.MethodGet, started.PlaybackPlan.Stream.URL, nil); status == http.StatusOK {
		t.Fatal("retired predecessor still serves")
	}
	if status, data = f.call(t, http.MethodGet, replanned.PlaybackPlan.Stream.URL, nil); status != http.StatusOK || !bytes.Contains(data, []byte("#EXTM3U")) {
		t.Fatalf("successor manifest: %d %s", status, data)
	}
	// Owner cleanup must close the current successor, not only the executor
	// captured when the initial owner callback was registered.
	value, ok := f.flow.owners.Load(sessionID)
	if !ok {
		t.Fatal("successor owner unavailable")
	}
	value.(*playback.RuntimeOwnerLeaseV3).Close()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for f.handler.TranscodeManager().GetTranscodeSession(sessionID) != nil {
		select {
		case <-deadline.C:
			t.Fatal("owner loss retained successor runtime")
		case <-poll.C:
		}
	}
	if _, err := f.handler.StopInitialPlayback(ctx, initialCallerV3(f), sessionID, PlaybackStopCommand{StopID: uuid.NewString()}); err != nil {
		t.Fatalf("stop: %v", err)
	}
}
