package handlers

// Bloem playback v3 handler coverage moved out of Silo's playback_v3_test.go so
// that file carries only the unavoidable Bloem seams.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

func TestTerminalAllowsAlternateFileV3IncludesSubtitleConversionIncompatibility(t *testing.T) {
	if !terminalAllowsAlternateFileV3(&playback.TerminalV3{Reason: "subtitle_conversion_unsupported"}) {
		t.Fatal(`terminal reason "subtitle_conversion_unsupported" should permit alternate selection`)
	}
}

// TestHandlePlaybackCapabilityV3EncodesTransformationsAsArray pins the check
// Bloem appended to Silo's TestHandlePlaybackCapabilityV3AdvertisesTheFinalizedContract.
func TestHandlePlaybackCapabilityV3EncodesTransformationsAsArray(t *testing.T) {
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playback/capability", nil).WithContext(newAuthorizedPlaybackContext())
	rr := httptest.NewRecorder()
	handler.HandlePlaybackCapabilityV3(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if string(document["transformations"]) == "null" {
		t.Fatalf("transformations wire value = null, want an array")
	}
}

func TestHandlePlaybackCapabilityV3EncodesNoTransformationsAsEmptyArray(t *testing.T) {
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	handler.v3RegistryProbe = func(context.Context, string, tonemap.Capabilities) (*playback.TransformationRegistryV3, error) {
		return playback.NewTransformationRegistryV3(nil), nil
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/playback/capability", nil).WithContext(newAuthorizedPlaybackContext())
	handler.HandlePlaybackCapabilityV3(recorder, request)
	var document map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if string(document["transformations"]) != "[]" {
		t.Fatalf("empty transformations wire value = %s, want []", document["transformations"])
	}
}

func TestHandlePlaybackCapabilityV3AdvertisesHeaderAuthenticationReadinessFromSettings(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  bool
	}{
		{"default disabled", "", false},
		{"explicit disabled", "disabled", false},
		{"enabled", "single_or_affine", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
			handler.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{"playback.header_authenticated_media_mode": test.value}}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/playback/capability", nil).WithContext(newAuthorizedPlaybackContext())
			rr := httptest.NewRecorder()
			handler.HandlePlaybackCapabilityV3(rr, req)
			var response playback.CapabilityResponseV3
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if got := playback.HasFeatureV3(response.Features, playback.FeatureHeaderAuthenticatedMediaReadyV3); got != test.want {
				t.Fatalf("readiness = %v, want %v; features = %v", got, test.want, response.Features)
			}
		})
	}
}

func TestHandleStartPlaybackV3AcceptsSiloAppleDraftV3Shape(t *testing.T) {
	file := v3HandlerFixtureFile(t)
	manager := playback.NewSessionManager(0, 0)
	handler := NewPlaybackHandler(manager, testPlaybackFileResolver{file: file})
	handler.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{"allow_4k_transcode": "true"}}
	handler.ItemAccess = allowAllPlaybackItemAccess{}

	start := v3HandlerStartRequest()
	start.PlaybackAttemptID = "apple:compat-attempt-0001"
	body := marshalV3StartRequest(t, start)
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	capabilities := payload["client_capabilities"].(map[string]any)
	delete(capabilities, "video_evidence")
	delete(capabilities, "audio_evidence")
	context := payload["client_playback_context"].(map[string]any)
	context["platform"] = "ios"
	deliveries := context["deliveries"].(map[string]any)
	context["engines"] = map[string]any{
		"media3_direct":            deliveries[playback.DeliveryClassOriginalHTTPV3],
		"media3_progressive_remux": deliveries[playback.DeliveryClassProgressiveV3],
		"media3_hls":               deliveries[playback.DeliveryClassHLSV3],
	}
	delete(context, "deliveries")
	device := context["device"].(map[string]any)
	delete(device, "platform")
	output := context["output"].(map[string]any)
	output["output_route_generation"] = 123
	delete(output, "output_context_id")
	legacyBody, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playback/start", bytes.NewReader(legacyBody))
	req = req.WithContext(newAuthorizedPlaybackContext())
	rr := httptest.NewRecorder()
	handler.HandleStartPlayback(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	plan := response["playback_plan"].(map[string]any)
	if got := plan["engine"]; got != "media3_direct" {
		t.Fatalf("engine = %v, want media3_direct", got)
	}
}
