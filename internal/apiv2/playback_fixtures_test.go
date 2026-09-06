package apiv2

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type fixturePlaybackService struct{ fakePlaybackService }

func (f *fixturePlaybackService) PlaybackCapabilities(ctx context.Context, userID int, profileID string) (handlers.PlaybackCapabilitiesView, error) {
	if profileID == "p-primary" {
		return handlers.PlaybackCapabilitiesView{Revision: "playback-unconfigured", State: "not_configured", ProtocolVersions: []int{}, Features: []string{}, Deliveries: []playback.DeliveryV3{}}, nil
	}
	view, err := f.fakePlaybackService.PlaybackCapabilities(ctx, userID, profileID)
	view.Features = fixtureInitialPlaybackFeatures()
	return view, err
}
func (f *fixturePlaybackService) StartInitialPlayback(ctx context.Context, caller handlers.PlaybackCaller, request playback.StartRequestV3) (playback.DecisionResponseV3, error) {
	if caller.InstallationID != playbackTestInstallation {
		return playback.DecisionResponseV3{}, &handlers.PlaybackOperationError{Status: 409, Code: "installation_changed", Message: "Playback installation changed; refresh capabilities"}
	}
	return f.fakePlaybackService.StartInitialPlayback(ctx, caller, request)
}
func (f *fixturePlaybackService) ApplyInitialProgress(_ context.Context, _ handlers.PlaybackCaller, _ string, command handlers.PlaybackProgressCommand) (handlers.PlaybackMutationView, error) {
	outcome := "applied"
	if command.Sequence < 42 {
		outcome = "stale_sample"
	}
	return handlers.PlaybackMutationView{Outcome: outcome, Accepted: &handlers.PlaybackAcceptedProgress{Sequence: 42, Position: 120, IsPaused: false}}, nil
}
func (f *fixturePlaybackService) StopInitialPlayback(_ context.Context, _ handlers.PlaybackCaller, _ string, command handlers.PlaybackStopCommand) (handlers.PlaybackMutationView, error) {
	draining := command.StopID == playbackTestStop
	outcome := "stopped"
	if draining {
		outcome = "draining"
	}
	return handlers.PlaybackMutationView{Outcome: outcome, Accepted: &handlers.PlaybackAcceptedProgress{Sequence: 42, Position: 120, IsPaused: false}, StopID: command.StopID, HistoryID: "33333333-3333-4333-8333-333333333333", Draining: draining}, nil
}
func fixturePlayback() PlaybackService {
	service := &fixturePlaybackService{}
	fixturePlaybackRead("decision_response.json", &service.response)
	service.response.ServerFeatures = fixtureInitialPlaybackFeatures()
	return service
}
func fixturePlaybackRead(name string, out any) {
	data, err := os.ReadFile("../playback/testdata/protocol_v3/" + name)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		panic(err)
	}
}
func fixturePlaybackJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
func playbackFixtureCases() []fixtureCase {
	var start map[string]any
	fixturePlaybackRead("start_request.json", &start)
	start["file_id"] = "42"
	start["profile_id"] = "p-owner"
	start["installation_id"] = playbackTestInstallation
	startBody := fixturePlaybackJSON(start)
	start["protocol_version"] = 2
	invalidBody := fixturePlaybackJSON(start)
	start["protocol_version"] = 3
	start["installation_id"] = "99999999-9999-4999-8999-999999999999"
	mismatchBody := fixturePlaybackJSON(start)
	session := "11111111-1111-4111-8111-111111111111"
	cases := []fixtureCase{}
	add := func(name, id, method, path, body, schema, scenario string, status int, headers map[string]string) {
		cases = append(cases, fixtureCase{name: name, operationID: id, method: method, path: Prefix + "/playback" + path, body: body, schema: "#/components/schemas/" + schema, scenario: scenario, status: status, headers: headers, assertHeaders: []string{"Content-Type", "Cache-Control"}})
	}
	add("playback_capability_unconfigured", "getPlaybackCapabilities", http.MethodGet, "/capabilities", "", "PlaybackCapabilities", "Playback remains discoverable while its runtime is not configured.", 200, with(bearer(adminToken), "X-Profile-Id", "p-primary"))
	add("playback_capability_available", "getPlaybackCapabilities", http.MethodGet, "/capabilities", "", "PlaybackCapabilities", "The admitted viewer receives a durable installation identifier and supported routes.", 200, viewerHeaders())
	add("playback_start_opaque_ids", "startPlayback", http.MethodPost, "/start", startBody, "PlaybackDecision", "A synthetic protocol-v3 plan represents every media file identifier as an opaque string.", 201, viewerHeaders())
	for _, sequence := range []int{42, 41} {
		name := "playback_progress_applied"
		if sequence == 41 {
			name = "playback_progress_stale"
		}
		add(name, "updatePlaybackProgress", http.MethodPost, "/"+session+"/progress", fixturePlaybackJSON(map[string]any{"installation_id": playbackTestInstallation, "sequence": sequence, "position": 120, "is_paused": false}), "PlaybackMutation", "Sequenced progress returns the committed accepted tuple, including after a stale sample.", 200, viewerHeaders())
	}
	for _, stop := range []struct {
		name, id string
		status   int
	}{{"playback_stop_draining", playbackTestStop, 202}, {"playback_stop_completed", "44444444-4444-4444-8444-444444444444", 200}} {
		add(stop.name, "stopPlayback", http.MethodDelete, "/"+session, fixturePlaybackJSON(map[string]any{"installation_id": playbackTestInstallation, "stop_id": stop.id}), "PlaybackMutation", "An exact stop returns its durable receipt while grants drain or after completion.", stop.status, viewerHeaders())
	}
	add("playback_installation_changed", "startPlayback", http.MethodPost, "/start", mismatchBody, "Problem", "A different installation requires capability discovery before starting a new attempt.", 409, viewerHeaders())
	add("playback_invalid_protocol", "startPlayback", http.MethodPost, "/start", invalidBody, "Problem", "Protocol-version validation uses the v2 422 status.", 422, viewerHeaders())
	return cases
}

// Match the initial direct/local-HLS feature contract rather than the broader
// historical v3 golden, which also describes replacement lifecycle operations.
func fixtureInitialPlaybackFeatures() []string {
	return []string{playback.FeaturePlaybackPlanV3, playback.FeatureNeutralContractV3,
		playback.FeatureLayoutPassthrough, playback.FeatureDeviceQuirksV3,
		playback.FeatureOutputDisplayEvidenceV3, playback.FeatureDirectStreamResumeV3,
		playback.FeatureSoftwareVideoDecodeV3, playback.FeaturePlanSourceDurationV3,
		"sequenced_progress_v1"}
}
