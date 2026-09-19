package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/livetv"
)

func TestBloemXtreamProviderEndpointFailClosed(t *testing.T) {
	h := NewLiveTVHandler(livetv.NewService(nil))
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"unavailable encrypted storage", `{"url":"https://provider.invalid","username":"private-user-marker","password":"private-password-marker"}`, 503},
		{"wrong source kind", `{"type":"hdhomerun","password":"private-password-marker"}`, 400},
		{"invalid JSON", `{"password":"private-password-marker"`, 400},
		{"oversized credential body", `{"password":"` + strings.Repeat("private-password-marker", 2000) + `"}`, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/bloem/v1/livetv/tuners/xtream", strings.NewReader(test.body))
			w := httptest.NewRecorder()
			h.HandleAddXtreamTuner(w, r)
			if w.Code != test.status {
				t.Fatalf("status=%d want=%d", w.Code, test.status)
			}
			if strings.Contains(w.Body.String(), "private-user-marker") || strings.Contains(w.Body.String(), "private-password-marker") {
				t.Fatal("provider endpoint reflected credentials")
			}
		})
	}
	w := httptest.NewRecorder()
	h.HandleAddTuner(w, httptest.NewRequest(http.MethodPost, "/api/bloem/v1/livetv/tuners", strings.NewReader(`{"type":"xtream","url":"https://provider.invalid"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatal("generic tuner endpoint accepted ambiguous Xtream setup")
	}
}

func TestBloemXtreamCapabilityAdvertisesSupportWithoutGrantOrMetadata(t *testing.T) {
	h := NewLiveTVHandler(livetv.NewService(nil))
	w := httptest.NewRecorder()
	h.HandleCapability(w, httptest.NewRequest(http.MethodGet, "/api/bloem/v1/livetv/capability", nil))
	var capability livetv.CapabilityResponse
	if err := json.Unmarshal(w.Body.Bytes(), &capability); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !capability.Supported || !capability.XtreamSupported || capability.Allowed || capability.Available {
		t.Fatalf("incorrect ungranted capability: %+v", capability)
	}
}
