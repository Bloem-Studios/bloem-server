package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/branding"
	"github.com/Silo-Server/silo-server/internal/serverid"
)

// fakeIdentitySettings stands in for the server_settings store. It reproduces
// the real repository's SetIfAbsent contract — a value lands only while the key
// is absent or empty — and can fail reads, because "answer 503 rather than
// serve an identifier that might change" is this endpoint's distinguishing
// behavior. The resolution algorithm itself is tested in internal/serverid.
type fakeIdentitySettings struct {
	mu               sync.Mutex
	values           map[string]string
	getErr           error
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
	if f.getErr != nil {
		return "", f.getErr
	}
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
// as setup_complete, and can fail the way a database read fails.
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

// routerWith mounts the public identity route the way the real router does:
// inside /api/bloem/v1, with no auth middleware. That the REAL router mounts it that
// way is asserted separately, in internal/api.
func routerWith(t *testing.T, settings *fakeIdentitySettings, setup fakeSetupState) http.Handler {
	t.Helper()
	var store serverid.Store
	if settings != nil {
		store = settings
	}
	identity := NewServerIdentityHandler(serverid.NewResolver(store), brandingServiceFor(settings), setup)
	router := chi.NewRouter()
	router.Route(NativeAPIPrefix, func(r chi.Router) {
		r.Get("/server/identity", identity.HandleGetServerIdentity)
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

	first := performJSONRequest(t, routerWith(t, settings, fakeSetupState{}), http.MethodGet, NativeAPIPrefix+"/server/identity")
	second := performJSONRequest(t, routerWith(t, settings, fakeSetupState{}), http.MethodGet, NativeAPIPrefix+"/server/identity")

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
	if stored := settings.values[serverid.SettingKey]; stored != serverID {
		t.Fatalf("stored instance id = %q, want the served %q", stored, serverID)
	}
	if _, setIfAbsentCalls := settings.writes(); setIfAbsentCalls != 1 {
		t.Fatalf("SetIfAbsent calls = %d, want 1 (minted once, then read back)", setIfAbsentCalls)
	}
}

func TestServerIdentityReusesPreExistingInstanceID(t *testing.T) {
	settings := newFakeSettings(t)
	settings.values[serverid.SettingKey] = "pre-existing-instance-id"

	response := performJSONRequest(t, routerWith(t, settings, fakeSetupState{}), http.MethodGet, NativeAPIPrefix+"/server/identity")

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

			response := performJSONRequest(t, routerWith(t, settings, setup), http.MethodGet, NativeAPIPrefix+"/server/identity")

			if response.Status != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Status, http.StatusOK)
			}
			versions, ok := response.Body["api_versions"].([]any)
			if !ok {
				t.Fatalf("api_versions = %v, want a list", response.Body["api_versions"])
			}
			served := make([]float64, 0, len(versions))
			for _, version := range versions {
				number, isNumber := version.(float64)
				if !isNumber {
					t.Fatalf("api_versions contains a non-number: %v", versions)
				}
				served = append(served, number)
			}
			// Both majors are mounted unconditionally: /api/v1 is the
			// Silo-compatible projection and /api/bloem/v1 is the native API.
			for _, want := range []float64{1, 2} {
				if !slices.Contains(served, want) {
					t.Errorf("api_versions = %v, want it to contain %v", served, want)
				}
			}
			if response.Body["setup_complete"] != tc.setupComplete {
				t.Fatalf("setup_complete = %v, want %v", response.Body["setup_complete"], tc.setupComplete)
			}
		})
	}
}

func TestServerIdentityServesTheOperatorFacingServerName(t *testing.T) {
	settings := newFakeSettings(t)

	defaulted := performJSONRequest(t, routerWith(t, settings, fakeSetupState{}), http.MethodGet, NativeAPIPrefix+"/server/identity")
	if defaulted.Body["server_name"] != branding.DefaultServerName {
		t.Fatalf("server_name = %v, want the branding default %q", defaulted.Body["server_name"], branding.DefaultServerName)
	}

	settings.values[branding.KeyServerName] = "Upstairs"
	named := performJSONRequest(t, routerWith(t, settings, fakeSetupState{}), http.MethodGet, NativeAPIPrefix+"/server/identity")
	if named.Body["server_name"] != "Upstairs" {
		t.Fatalf("server_name = %v, want the configured branding name", named.Body["server_name"])
	}
}

// The three ways this endpoint can fail all end the same way: 503 unavailable,
// never a body carrying an identifier the server cannot stand behind.
func TestServerIdentityRefusesRatherThanGuess(t *testing.T) {
	failingSettings := newFakeSettings(t)
	failingSettings.getErr = errors.New("transient read failure")

	for _, tc := range []struct {
		name     string
		settings *fakeIdentitySettings
		setup    fakeSetupState
	}{
		{name: "no settings store", settings: nil, setup: fakeSetupState{}},
		{name: "identifier read fails", settings: failingSettings, setup: fakeSetupState{}},
		{name: "setup state read fails", settings: newFakeSettings(t), setup: fakeSetupState{err: errors.New("transient read failure")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := performJSONRequest(t, routerWith(t, tc.settings, tc.setup), http.MethodGet, NativeAPIPrefix+"/server/identity")

			if response.Status != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", response.Status, http.StatusServiceUnavailable)
			}
			if response.Body["error"] != "unavailable" {
				t.Fatalf("error = %v, want unavailable", response.Body["error"])
			}
			if _, present := response.Body["server_id"]; present {
				t.Fatalf("failure body carries a server_id: %v", response.Body)
			}
		})
	}
}

func TestServerIdentityIsUnavailableWithoutASetupReporter(t *testing.T) {
	identity := NewServerIdentityHandler(serverid.NewResolver(newFakeSettings(t)), nil, nil)
	rec := httptest.NewRecorder()

	identity.HandleGetServerIdentity(rec, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/server/identity", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
