package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/branding"
)

// fakeIdentitySettings stands in for the server_settings store. It reproduces
// the real repository's SetIfAbsent contract — a value lands only while the key
// is absent or empty, and the caller is told whether it won the write — so a
// test can prove the instance identifier is minted exactly once.
type fakeIdentitySettings struct {
	mu               sync.Mutex
	values           map[string]string
	setCalls         int
	setIfAbsentCalls int
}

func newFakeSettings(t *testing.T) *fakeIdentitySettings {
	t.Helper()
	return &fakeIdentitySettings{values: map[string]string{}}
}

func (f *fakeIdentitySettings) Get(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[key], nil
}

func (f *fakeIdentitySettings) Set(_ context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	f.values[key] = value
	return nil
}

func (f *fakeIdentitySettings) SetIfAbsent(_ context.Context, key, value string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setIfAbsentCalls++
	if f.values[key] != "" {
		return false, nil
	}
	f.values[key] = value
	return true, nil
}

func (f *fakeIdentitySettings) writes() (setCalls, setIfAbsentCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setCalls, f.setIfAbsentCalls
}

// fakeSetupState reports the initial-setup state the identity endpoint projects
// as setup_complete.
type fakeSetupState struct {
	needsSetup bool
	err        error
}

func (f fakeSetupState) NeedsSetup(context.Context) (bool, error) { return f.needsSetup, f.err }

// jsonResponse is a decoded endpoint answer.
type jsonResponse struct {
	Status int
	Body   map[string]any
}

// performJSONRequest issues an unauthenticated request and decodes the JSON
// body. Nothing sets an Authorization header, which is the point: both
// endpoints must answer a client that has never logged in.
func performJSONRequest(t *testing.T, handler http.Handler, method, path string) jsonResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	response := jsonResponse{Status: rec.Code}
	if err := json.Unmarshal(rec.Body.Bytes(), &response.Body); err != nil {
		t.Fatalf("decode %s %s body %q: %v", method, path, rec.Body.String(), err)
	}
	return response
}

// routerWith mounts the public identity and capability routes exactly as the
// real router does: inside /api/v1, ahead of the auth routes, with no auth
// middleware.
func routerWith(t *testing.T, settings *fakeIdentitySettings, setup fakeSetupState) http.Handler {
	t.Helper()
	var store ServerIdentitySettings
	if settings != nil {
		store = settings
	}
	identity := NewServerIdentityHandler(store, brandingServiceFor(settings), setup)
	capabilities := NewCapabilitiesHandler()
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) {
		r.Get("/server/identity", identity.HandleGetServerIdentity)
		r.Get("/capabilities", capabilities.HandleGetCapabilities)
	})
	return router
}

func brandingServiceFor(settings *fakeIdentitySettings) *branding.Service {
	if settings == nil {
		return nil
	}
	return branding.NewService(settings, nil)
}

func TestServerIdentityIsStableAndPublic(t *testing.T) {
	settings := newFakeSettings(t)

	first := performJSONRequest(t, routerWith(t, settings, fakeSetupState{}), http.MethodGet, "/api/v1/server/identity")
	second := performJSONRequest(t, routerWith(t, settings, fakeSetupState{}), http.MethodGet, "/api/v1/server/identity")

	if first.Status != http.StatusOK || second.Status != http.StatusOK {
		t.Fatalf("status = %d then %d, want %d twice", first.Status, second.Status, http.StatusOK)
	}
	serverID, _ := first.Body["server_id"].(string)
	if serverID == "" {
		t.Fatalf("server_id is empty: %v", first.Body)
	}
	if second.Body["server_id"] != first.Body["server_id"] {
		t.Fatalf("server identity is not stable: %v then %v", first.Body, second.Body)
	}
	if stored := settings.values[SettingServerInstanceID]; stored != serverID {
		t.Fatalf("stored instance id = %q, want the served %q", stored, serverID)
	}
	if _, setIfAbsentCalls := settings.writes(); setIfAbsentCalls != 1 {
		t.Fatalf("SetIfAbsent calls = %d, want 1 (minted once, then read back)", setIfAbsentCalls)
	}
}

func TestServerIdentityReusesPreExistingInstanceID(t *testing.T) {
	settings := newFakeSettings(t)
	settings.values[SettingServerInstanceID] = "pre-existing-instance-id"

	response := performJSONRequest(t, routerWith(t, settings, fakeSetupState{}), http.MethodGet, "/api/v1/server/identity")

	if response.Body["server_id"] != "pre-existing-instance-id" {
		t.Fatalf("server_id = %v, want the pre-existing row", response.Body["server_id"])
	}
	setCalls, setIfAbsentCalls := settings.writes()
	if setCalls != 0 || setIfAbsentCalls != 0 {
		t.Fatalf("writes = %d Set / %d SetIfAbsent, want none against an existing identifier", setCalls, setIfAbsentCalls)
	}
}

func TestServerIdentityReportsAPIVersionsAndSetupState(t *testing.T) {
	for _, tc := range []struct {
		name          string
		needsSetup    bool
		setupComplete bool
	}{
		{name: "fresh install", needsSetup: true, setupComplete: false},
		{name: "configured install", needsSetup: false, setupComplete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := newFakeSettings(t)
			setup := fakeSetupState{needsSetup: tc.needsSetup}

			response := performJSONRequest(t, routerWith(t, settings, setup), http.MethodGet, "/api/v1/server/identity")

			if response.Status != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Status, http.StatusOK)
			}
			versions, ok := response.Body["api_versions"].([]any)
			if !ok || len(versions) == 0 {
				t.Fatalf("api_versions = %v, want a non-empty list", response.Body["api_versions"])
			}
			found := false
			for _, version := range versions {
				if number, isNumber := version.(float64); isNumber && number == 1 {
					found = true
				}
			}
			if !found {
				t.Fatalf("api_versions = %v, want it to contain 1", versions)
			}
			if response.Body["setup_complete"] != tc.setupComplete {
				t.Fatalf("setup_complete = %v, want %v", response.Body["setup_complete"], tc.setupComplete)
			}
		})
	}
}

func TestServerIdentityServesTheOperatorFacingServerName(t *testing.T) {
	settings := newFakeSettings(t)

	defaulted := performJSONRequest(t, routerWith(t, settings, fakeSetupState{}), http.MethodGet, "/api/v1/server/identity")
	if defaulted.Body["server_name"] != branding.DefaultServerName {
		t.Fatalf("server_name = %v, want the branding default %q", defaulted.Body["server_name"], branding.DefaultServerName)
	}

	settings.values[branding.KeyServerName] = "Upstairs"
	named := performJSONRequest(t, routerWith(t, settings, fakeSetupState{}), http.MethodGet, "/api/v1/server/identity")
	if named.Body["server_name"] != "Upstairs" {
		t.Fatalf("server_name = %v, want the configured branding name", named.Body["server_name"])
	}
}

func TestServerIdentityIsUnavailableWithoutAStore(t *testing.T) {
	response := performJSONRequest(t, routerWith(t, nil, fakeSetupState{}), http.MethodGet, "/api/v1/server/identity")

	if response.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: an unstable identifier is worse than none", response.Status, http.StatusServiceUnavailable)
	}
	if response.Body["error"] != "unavailable" {
		t.Fatalf("error = %v, want unavailable", response.Body["error"])
	}
}

func TestServerIdentityMintsOneIdentifierUnderConcurrency(t *testing.T) {
	settings := newFakeSettings(t)
	handler := routerWith(t, settings, fakeSetupState{})

	const callers = 8
	ids := make([]string, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range ids {
		go func(slot int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/server/identity", nil))
			var body struct {
				ServerID string `json:"server_id"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			ids[slot] = body.ServerID
		}(i)
	}
	wg.Wait()

	for _, id := range ids {
		if id == "" || id != ids[0] {
			t.Fatalf("concurrent server_id values disagree: %v", ids)
		}
	}
	if stored := settings.values[SettingServerInstanceID]; stored != ids[0] {
		t.Fatalf("stored instance id = %q, want the served %q", stored, ids[0])
	}
}
