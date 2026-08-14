package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestServerIdentityAndCapabilitiesAreMountedPublicly exercises the REAL router,
// not a replica of it. Both endpoints are what a client reaches before it holds
// any credentials, so a middleware later wrapping the /api/v1 group — or the
// routes drifting behind an auth group — would break every client while a
// handler-level suite stayed green.
func TestServerIdentityAndCapabilitiesAreMountedPublicly(t *testing.T) {
	router := NewRouter(Dependencies{})

	capabilities := httptest.NewRecorder()
	router.ServeHTTP(capabilities, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))
	if capabilities.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d, want %d (no Authorization header was sent)", capabilities.Code, http.StatusOK)
	}
	var advertised struct {
		SchemaVersion int      `json:"schema_version"`
		MediaTypes    []string `json:"media_types"`
		Features      []string `json:"features"`
	}
	if err := json.Unmarshal(capabilities.Body.Bytes(), &advertised); err != nil {
		t.Fatalf("decode capabilities %q: %v", capabilities.Body.String(), err)
	}
	if advertised.SchemaVersion != 1 || len(advertised.MediaTypes) == 0 || len(advertised.Features) == 0 {
		t.Fatalf("capabilities body = %s, want the populated contract shape", capabilities.Body.String())
	}

	// Dependencies{} has no database, so there is no settings store to resolve a
	// stable identifier from. The route must still be mounted and must refuse
	// rather than invent one.
	identity := httptest.NewRecorder()
	router.ServeHTTP(identity, httptest.NewRequest(http.MethodGet, "/api/v1/server/identity", nil))
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
