package handlers

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func capabilityStrings(t *testing.T, body map[string]any, field string) []string {
	t.Helper()
	raw, ok := body[field].([]any)
	if !ok {
		t.Fatalf("%s = %v, want a list", field, body[field])
	}
	values := make([]string, 0, len(raw))
	for _, entry := range raw {
		value, isString := entry.(string)
		if !isString || value == "" {
			t.Fatalf("%s contains a non-string or empty entry: %v", field, raw)
		}
		values = append(values, value)
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			t.Fatalf("%s repeats %q: %v", field, value, values)
		}
		seen[value] = true
	}
	return values
}

func TestCapabilitiesAnswerTheContractShapeWithoutAuthentication(t *testing.T) {
	response := performJSONRequest(t, routerWith(t, newFakeSettings(t), fakeSetupState{}), http.MethodGet, "/api/v1/capabilities")

	if response.Status != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Status, http.StatusOK)
	}
	if response.Body["schema_version"] != float64(1) {
		t.Fatalf("schema_version = %v, want 1", response.Body["schema_version"])
	}
	mediaTypes := capabilityStrings(t, response.Body, "media_types")
	for _, want := range []string{"movie", "series", "episode", "audiobook", "ebook", "manga"} {
		if !slices.Contains(mediaTypes, want) {
			t.Errorf("media_types is missing %q: %v", want, mediaTypes)
		}
	}
	if slices.Contains(mediaTypes, "video") {
		t.Errorf("media_types advertises the internal %q scope: %v", "video", mediaTypes)
	}
}

func TestCapabilitiesAdvertiseThisMilestonesFeatureTokens(t *testing.T) {
	response := performJSONRequest(t, routerWith(t, newFakeSettings(t), fakeSetupState{}), http.MethodGet, "/api/v1/capabilities")

	features := capabilityStrings(t, response.Body, "features")
	for _, want := range []string{"watch_document_v1", "device_pairing_v1", "progress_sync_v1"} {
		if !slices.Contains(features, want) {
			t.Errorf("features is missing %q: %v", want, features)
		}
	}
}

func TestCapabilitiesAdvertiseTheExistingPlaybackAndEventsTokens(t *testing.T) {
	response := performJSONRequest(t, routerWith(t, newFakeSettings(t), fakeSetupState{}), http.MethodGet, "/api/v1/capabilities")

	features := capabilityStrings(t, response.Body, "features")
	for _, want := range playback.ServerFeaturesV3() {
		if !slices.Contains(features, want) {
			t.Errorf("features is missing the playback token %q: %v", want, features)
		}
	}
	if !slices.Contains(features, "declared_event_channels") {
		t.Errorf("features is missing the events token %q: %v", "declared_event_channels", features)
	}
}

func TestCapabilitiesResponsesDoNotShareMutableState(t *testing.T) {
	handler := NewCapabilitiesHandler()

	first := handler.capabilities()
	first.Features[0] = "mutated"
	first.MediaTypes[0] = "mutated"
	second := handler.capabilities()

	if second.Features[0] == "mutated" || second.MediaTypes[0] == "mutated" {
		t.Fatalf("capability response leaks its backing arrays: %v", second)
	}
}
