package apiv2

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type timelinePlaybackService struct {
	manifest playback.ClientPlaybackManifestV3
	*fakePlaybackService
	enabled bool
}

func (f *timelinePlaybackService) GetClientPlaybackTimeline(context.Context, handlers.PlaybackCaller, int) (playback.ClientPlaybackManifestV3, error) {
	return f.manifest, nil
}
func (f *timelinePlaybackService) SupportsBoundClientTimeline() bool { return f.enabled }
func (f *timelinePlaybackService) PlaybackCapabilities(ctx context.Context, u int, p string) (handlers.PlaybackCapabilitiesView, error) {
	v, err := f.fakePlaybackService.PlaybackCapabilities(ctx, u, p)
	v.Features = append(v.Features, playback.FeatureBoundClientTimelineV3)
	return v, err
}

func TestPlaybackClientTimelineWireAndSafeGate(t *testing.T) {
	timeline, err := playback.NewClientPlaybackTimelineV3("book", "edition", []playback.ClientPlaybackPartV3{{FileID: 41, DurationSeconds: 600}, {FileID: 42, DurationSeconds: 400}}, 42)
	if err != nil {
		t.Fatal(err)
	}
	service := &timelinePlaybackService{fakePlaybackService: &fakePlaybackService{response: playback.DecisionResponseV3{ProtocolVersion: 3, Outcome: playback.OutcomePlayableV3, SessionID: playbackTestStop, ProgressTimeline: &timeline}, mutation: handlers.PlaybackMutationView{Outcome: "applied", Accepted: &handlers.PlaybackAcceptedProgress{Sequence: 2, Position: 30, TimelineID: timeline.TimelineID, ItemPosition: new(float64(630))}}}}
	service.manifest, _ = playback.NewClientPlaybackManifestV3("book", "edition", []playback.ClientPlaybackPartV3{{FileID: 41, DurationSeconds: 600}, {FileID: 42, DurationSeconds: 400}})
	deps, _ := catalogDeps(t)
	deps.Playback = service
	h := newTestHandler(t, deps)
	body := playbackStartFixture(t)
	body["progress_persistence"] = "client_bound"
	body["timeline_id"] = timeline.TimelineID
	body["start_position"] = 30
	body["client_features"] = append(body["client_features"].([]any), playback.FeatureBoundClientTimelineV3)
	cap := do(t, h, http.MethodGet, Prefix+"/playback/capabilities", "", viewerHeaders())
	if cap.Code != 200 || strings.Contains(cap.Body.String(), playback.FeatureBoundClientTimelineV3) {
		t.Fatalf("incomplete capability advertised: %d %s", cap.Code, cap.Body.String())
	}
	requireProblem(t, do(t, h, http.MethodPost, Prefix+"/playback/start", playbackJSON(t, body), viewerHeaders()), TypeCapabilityUnsupported)
	if service.calls != 0 {
		t.Fatal("unconfigured start dispatched")
	}
	progress := map[string]any{"installation_id": playbackTestInstallation, "timeline_id": timeline.TimelineID, "sequence": 2, "position": 30, "is_paused": false}
	requireProblem(t, do(t, h, http.MethodPost, Prefix+"/playback/"+playbackTestStop+"/progress", playbackJSON(t, progress), viewerHeaders()), TypeCapabilityUnsupported)
	service.enabled = true
	discovered := do(t, h, http.MethodGet, Prefix+"/playback/timelines/42?installation_id="+playbackTestInstallation, "", viewerHeaders())
	if discovered.Code != 200 || discovered.Header().Get("Cache-Control") != "no-store" || !strings.Contains(discovered.Body.String(), `"offset_seconds":600`) || !strings.Contains(discovered.Body.String(), `"file_id":"41"`) {
		t.Fatalf("manifest discovery: %d %s", discovered.Code, discovered.Body.String())
	}
	started := do(t, h, http.MethodPost, Prefix+"/playback/start", playbackJSON(t, body), viewerHeaders())
	if started.Code != 201 || service.request.ProgressPersistence != playback.ProgressPersistenceClientBoundV3 || !strings.Contains(started.Body.String(), `"file_id":"42"`) || !strings.Contains(started.Body.String(), `"part_offset_seconds":600`) {
		t.Fatalf("start mapping: %d %s", started.Code, started.Body.String())
	}
	updated := do(t, h, http.MethodPost, Prefix+"/playback/"+playbackTestStop+"/progress", playbackJSON(t, progress), viewerHeaders())
	if updated.Code != 200 || service.progress.TimelineID != timeline.TimelineID || service.progress.Position != 30 || !strings.Contains(updated.Body.String(), `"item_position":630`) || !strings.Contains(updated.Body.String(), `"position":30`) {
		t.Fatalf("progress mapping: %d %s", updated.Code, updated.Body.String())
	}
	stop := map[string]any{"installation_id": playbackTestInstallation, "timeline_id": timeline.TimelineID, "stop_id": playbackTestStop}
	stopped := do(t, h, http.MethodDelete, Prefix+"/playback/"+playbackTestStop, playbackJSON(t, stop), viewerHeaders())
	if stopped.Code != 200 || service.stop.TimelineID != timeline.TimelineID {
		t.Fatalf("stop mapping: %d %s", stopped.Code, stopped.Body.String())
	}
	before := service.calls
	progress["timeline_id"] = "changed"
	bad := do(t, h, http.MethodPost, Prefix+"/playback/"+playbackTestStop+"/progress", playbackJSON(t, progress), viewerHeaders())
	if bad.Code != 422 || service.calls != before {
		t.Fatalf("invalid timeline dispatched: %d %s", bad.Code, bad.Body.String())
	}
	service.mutation.Accepted.ItemPosition = new(float64(0))
	zero := do(t, h, http.MethodDelete, Prefix+"/playback/"+playbackTestStop, playbackJSON(t, stop), viewerHeaders())
	if !strings.Contains(zero.Body.String(), `"item_position":0`) {
		t.Fatalf("zero global position omitted: %s", zero.Body.String())
	}
}
