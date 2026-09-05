package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/livetv"
)

type discoveryTVStore struct {
	fakeLiveTVStore
	channels []livetv.Channel
}

func (s *discoveryTVStore) ListChannels(context.Context, string) ([]livetv.Channel, error) {
	return s.channels, nil
}
func TestLiveTVCapabilityViewerAvailability(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		allowed, enabled, available bool
	}{
		{"denied", false, true, false}, {"empty", true, false, false}, {"ready", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewLiveTVHandler(livetv.NewServiceWithStore(&discoveryTVStore{channels: []livetv.Channel{{Enabled: tc.enabled}}}))
			req := httptest.NewRequest("GET", "/api/v1/livetv/capability", nil)
			req = req.WithContext(access.SetScope(req.Context(), access.Scope{LiveTVAllowed: tc.allowed}))
			rec := httptest.NewRecorder()
			h.HandleCapability(rec, req)
			var response struct {
				Supported, Allowed, Available bool
				Heartbeat                     int `json:"heartbeat_interval_seconds"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if rec.Code != 200 || !response.Supported || response.Allowed != tc.allowed || response.Available != tc.available || response.Heartbeat != 30 {
				t.Fatalf("unexpected capability: %s", rec.Body.String())
			}
		})
	}
}
