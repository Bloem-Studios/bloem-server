package apiv2

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/playback"
)

const playbackTestInstallation = "11111111-1111-4111-8111-111111111111"
const playbackTestStop = "22222222-2222-4222-8222-222222222222"

type fakePlaybackService struct {
	calls    int
	caller   handlers.PlaybackCaller
	request  playback.StartRequestV3
	progress handlers.PlaybackProgressCommand
	stop     handlers.PlaybackStopCommand
	event    handlers.PlaybackRouteEventCommand
	session  string
	response playback.DecisionResponseV3
	mutation handlers.PlaybackMutationView
	err      error
}

func (f *fakePlaybackService) PlaybackCapabilities(context.Context, int, string) (handlers.PlaybackCapabilitiesView, error) {
	return handlers.PlaybackCapabilitiesView{InstallationID: playbackTestInstallation, Revision: "cap-1", State: "available", Allowed: true, ProtocolVersions: []int{3}, Features: []string{"sequenced_progress_v1"}, Deliveries: []playback.DeliveryV3{playback.DeliveryOriginalHTTPV3, playback.DeliveryTranscodeHLSV3}}, f.err
}
func (f *fakePlaybackService) StartInitialPlayback(_ context.Context, caller handlers.PlaybackCaller, request playback.StartRequestV3) (playback.DecisionResponseV3, error) {
	f.calls++
	f.caller = caller
	f.request = request
	return f.response, f.err
}
func (f *fakePlaybackService) ApplyInitialProgress(_ context.Context, caller handlers.PlaybackCaller, session string, command handlers.PlaybackProgressCommand) (handlers.PlaybackMutationView, error) {
	f.calls++
	f.caller = caller
	f.session = session
	f.progress = command
	return f.mutation, f.err
}
func (f *fakePlaybackService) ReportInitialRouteEvent(_ context.Context, caller handlers.PlaybackCaller, command handlers.PlaybackRouteEventCommand) error {
	f.calls++
	f.caller = caller
	f.event = command
	return f.err
}
func (f *fakePlaybackService) StopInitialPlayback(_ context.Context, caller handlers.PlaybackCaller, session string, command handlers.PlaybackStopCommand) (handlers.PlaybackMutationView, error) {
	f.calls++
	f.caller = caller
	f.session = session
	f.stop = command
	return f.mutation, f.err
}

func playbackStartFixture(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("../playback/testdata/protocol_v3/start_request.json")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	body["file_id"] = "42"
	body["profile_id"] = "p-owner"
	body["installation_id"] = playbackTestInstallation
	return body
}
func playbackJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPlaybackV2CapabilitiesAndMissingConfiguration(t *testing.T) {
	deps, _ := catalogDeps(t)
	h := newTestHandler(t, deps)
	response := do(t, h, http.MethodGet, Prefix+"/playback/capabilities", "", viewerHeaders())
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"state":"not_configured"`) || !strings.Contains(response.Body.String(), `"allowed":false`) || strings.Contains(response.Body.String(), ":null") {
		t.Fatalf("unconfigured capability: %d %s", response.Code, response.Body.String())
	}
	requireProblem(t, do(t, h, http.MethodPost, Prefix+"/playback/start", playbackJSON(t, playbackStartFixture(t)), viewerHeaders()), TypeCapabilityNotConfigured)
	deps.Playback = &fakePlaybackService{}
	response = do(t, newTestHandler(t, deps), http.MethodGet, Prefix+"/playback/capabilities", "", viewerHeaders())
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"installation_id":"`+playbackTestInstallation+`"`) || !strings.Contains(response.Body.String(), `"allowed":true`) {
		t.Fatalf("capability: %d %s", response.Code, response.Body.String())
	}
}

func TestPlaybackV2StartUsesTypedServiceAndOpaqueIDs(t *testing.T) {
	deps, _ := catalogDeps(t)
	fake := &fakePlaybackService{}
	deps.Playback = fake
	data, err := os.ReadFile("../playback/testdata/protocol_v3/decision_response.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fake.response); err != nil {
		t.Fatal(err)
	}
	response := do(t, newTestHandler(t, deps), http.MethodPost, Prefix+"/playback/start", playbackJSON(t, playbackStartFixture(t)), viewerHeaders())
	if response.Code != 201 {
		t.Fatalf("start: %d %s", response.Code, response.Body.String())
	}
	if fake.calls != 1 || fake.request.FileID != 42 || fake.caller.ProfileID != "p-owner" || fake.caller.UserID <= 0 || fake.caller.InstallationID != playbackTestInstallation {
		t.Fatalf("service caller/request: %+v %+v", fake.caller, fake.request)
	}
	var body struct {
		Plan struct {
			Requested string `json:"requested_media_file_id"`
			Effective string `json:"effective_media_file_id"`
			Source    struct {
				FileID string `json:"media_file_id"`
			} `json:"source"`
		} `json:"playback_plan"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Plan.Requested == "" || body.Plan.Effective == "" || body.Plan.Source.FileID == "" {
		t.Fatalf("opaque IDs: %v %s", err, response.Body.String())
	}
}

func TestPlaybackV2RejectsInvalidInputBeforeService(t *testing.T) {
	for _, variation := range []string{"numeric file", "protocol", "profile", "installation", "unknown field", "null start"} {
		t.Run(variation, func(t *testing.T) {
			deps, _ := catalogDeps(t)
			fake := &fakePlaybackService{}
			deps.Playback = fake
			body := playbackStartFixture(t)
			switch variation {
			case "numeric file":
				body["file_id"] = 42
			case "protocol":
				body["protocol_version"] = 2
			case "profile":
				body["profile_id"] = "other"
			case "installation":
				body["installation_id"] = ""
			case "unknown field":
				body["unexpected"] = true
			case "null start":
				body["start_position"] = nil
			}
			response := do(t, newTestHandler(t, deps), http.MethodPost, Prefix+"/playback/start", playbackJSON(t, body), viewerHeaders())
			if response.Code != 422 {
				t.Fatalf("validation: %d %s", response.Code, response.Body.String())
			}
			if fake.calls != 0 {
				t.Fatal("invalid request reached application")
			}
		})
	}
}

func TestPlaybackV2ProgressAndStop(t *testing.T) {
	deps, _ := catalogDeps(t)
	fake := &fakePlaybackService{mutation: handlers.PlaybackMutationView{Outcome: "applied"}}
	deps.Playback = fake
	h := newTestHandler(t, deps)
	response := do(t, h, http.MethodPost, Prefix+"/playback/session-1/progress", playbackJSON(t, map[string]any{"installation_id": playbackTestInstallation, "sequence": 42, "position": 0, "is_paused": true}), viewerHeaders())
	if response.Code != 200 || fake.progress.Sequence != 42 || fake.progress.Position != 0 || !fake.progress.IsPaused {
		t.Fatalf("progress: %d %s %+v", response.Code, response.Body.String(), fake.progress)
	}
	fake.mutation = handlers.PlaybackMutationView{Outcome: "draining", StopID: playbackTestStop, Draining: true}
	stopBody := map[string]any{"installation_id": playbackTestInstallation, "stop_id": playbackTestStop}
	response = do(t, h, http.MethodDelete, Prefix+"/playback/session-1", playbackJSON(t, stopBody), viewerHeaders())
	if response.Code != 202 || fake.stop.Position != nil || fake.stop.Sequence != 0 || !strings.Contains(response.Body.String(), `"stop_id":"`+playbackTestStop+`"`) {
		t.Fatalf("stop: %d %s %+v", response.Code, response.Body.String(), fake.stop)
	}
	stopBody["sequence"] = 43
	stopBody["position"] = 0
	fake.mutation.Draining = false
	fake.mutation.Outcome = "stopped"
	response = do(t, h, http.MethodDelete, Prefix+"/playback/session-1", playbackJSON(t, stopBody), viewerHeaders())
	if response.Code != 200 || fake.stop.Position == nil || *fake.stop.Position != 0 || fake.stop.Sequence != 43 {
		t.Fatalf("final zero: %d %s %+v", response.Code, response.Body.String(), fake.stop)
	}
	fake.err = &handlers.PlaybackOperationError{Status: 400, Code: "bad_request", Message: "Invalid playback sample"}
	requireProblem(t, do(t, h, http.MethodDelete, Prefix+"/playback/session-1", playbackJSON(t, stopBody), viewerHeaders()), TypeValidationFailed)
}

func TestPlaybackV2CapabilityDisabledProblem(t *testing.T) {
	problem := playbackProblem(&handlers.PlaybackOperationError{Status: http.StatusConflict, Code: "capability_disabled", Message: "Playback source is not admitted"})
	if problem.Status != 409 || problem.Type != TypeCapabilityDisabled.URI() {
		t.Fatalf("capability problem: %+v", problem)
	}
}
