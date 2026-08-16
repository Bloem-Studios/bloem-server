package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// TestServerIdentityAndCapabilitiesAreMountedPublicly exercises the REAL router,
// not a replica of it. Both endpoints are what a client reaches before it holds
// any credentials, so a middleware later wrapping the /api/v2 group — or the
// routes drifting behind an auth group — would break every client while a
// handler-level suite stayed green.
func TestServerIdentityAndCapabilitiesAreMountedPublicly(t *testing.T) {
	router := NewRouter(Dependencies{})

	capabilities := httptest.NewRecorder()
	router.ServeHTTP(capabilities, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))
	if capabilities.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d, want %d (no Authorization header was sent)", capabilities.Code, http.StatusOK)
	}
	var advertised struct {
		API           string   `json:"api"`
		MediaTypes    []string `json:"media_types"`
		FeatureTokens []string `json:"feature_tokens"`
	}
	if err := json.Unmarshal(capabilities.Body.Bytes(), &advertised); err != nil {
		t.Fatalf("decode capabilities %q: %v", capabilities.Body.String(), err)
	}
	if advertised.API != "v2" || len(advertised.MediaTypes) == 0 {
		t.Fatalf("capabilities body = %s, want the populated contract shape", capabilities.Body.String())
	}
	// The capability document is a property of the build, so a router with no
	// dependencies at all still advertises the whole client surface. A client
	// that cannot find these tokens turns the features off.
	for _, token := range []string{"watch_document_v1", "device_pairing_v1", "progress_sync_v1"} {
		if !slices.Contains(advertised.FeatureTokens, token) {
			t.Errorf("feature_tokens = %v, want it to contain %q", advertised.FeatureTokens, token)
		}
	}

	// Dependencies{} has no database, so there is no settings store to resolve a
	// stable identifier from. The route must still be mounted and must refuse
	// rather than invent one — or 404, which a client reads as "this server is
	// too old to have an identity" instead of "ask again later".
	identity := httptest.NewRecorder()
	router.ServeHTTP(identity, httptest.NewRequest(http.MethodGet, "/api/v2/server/identity", nil))
	if identity.Code != http.StatusServiceUnavailable {
		t.Fatalf("identity status = %d, want %d", identity.Code, http.StatusServiceUnavailable)
	}
	var refusal struct {
		Error    string `json:"error"`
		ServerID string `json:"server_id"`
	}
	if err := json.Unmarshal(identity.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("decode identity %q: %v", identity.Body.String(), err)
	}
	if refusal.Error != "unavailable" || refusal.ServerID != "" {
		t.Fatalf("identity body = %s, want an unavailable refusal with no identifier", identity.Body.String())
	}
}

// The Silo-compatible projection must not grow either of them. A client that
// finds /api/v1/capabilities is talking to a different server than this one.
func TestTheNativeProbesAreNotServedOnV1(t *testing.T) {
	router := NewRouter(Dependencies{})

	for _, path := range []string{"/api/v1/capabilities", "/api/v1/server/identity"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want %d: the native surface lives on /api/v2", path, rec.Code, http.StatusNotFound)
		}
	}
}
