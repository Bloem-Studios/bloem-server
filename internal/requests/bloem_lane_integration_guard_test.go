package requests

import (
	"context"
	"errors"
	"testing"
)

// laneStore names the plugin behind each installation, as the repository does.
type laneStore struct {
	*fakeStore
	plugins map[int]string
}

func (s laneStore) InstallationPluginID(_ context.Context, id int) (string, error) {
	plugin, ok := s.plugins[id]
	if !ok {
		return "", ErrNotFound
	}
	return plugin, nil
}

func audiobookLaneInst(id string, install int) Integration {
	in := routerInstOn(id, install)
	in.BaseURL = ""
	in.APIKeyRef = "abk1.e30"
	in.CapabilityID = "audiobooks"
	in.SupportedMediaTypes = []string{"audiobook"}
	in.PluginConfig = map[string]any{"qbittorrent_url": "https://client.example.test"}
	return in
}

func isLaneRefusal(err error) bool {
	var validation *ValidationError
	return errors.As(err, &validation) && validation.FormError == errBloemLaneIntegration.FormError
}

// A generic edit cannot repoint an audiobook connection's stored credentials:
// base_url is empty there and the bound URLs live in plugin_config, so Silo's
// own "re-enter the key when base_url changes" rule never fires.
func TestGenericEditsRefuseAudiobookLaneConnections(t *testing.T) {
	store := newFakeStore()
	stored := audiobookLaneInst("abk-1", 7)
	store.integrations = []Integration{stored, routerInstOn("radarr-1", 1)}
	router := &fakeRouterProvider{}
	service := newTestService(store)
	service.SetRouterProvider(router)
	admin := Viewer{UserID: 1, IsAdmin: true}

	redirected := stored
	redirected.APIKeyRef = ""
	redirected.PluginConfig = map[string]any{"qbittorrent_url": "https://attacker.example.test"}
	if _, err := service.UpdateIntegration(context.Background(), admin, redirected); !isLaneRefusal(err) {
		t.Fatalf("v1 update err = %v, want lane refusal", err)
	}
	if _, err := service.UpdateIntegrationConditional(context.Background(), admin, redirected, -1); !isLaneRefusal(err) {
		t.Fatalf("v2 update err = %v, want lane refusal", err)
	}
	// Dropping the media type does not escape: the stored row decides.
	redirected.SupportedMediaTypes = nil
	if _, err := service.UpdateIntegration(context.Background(), admin, redirected); !isLaneRefusal(err) {
		t.Fatalf("media-type rewrite err = %v, want lane refusal", err)
	}
	if _, err := service.LoadIntegrationOptions(context.Background(), admin, Integration{ID: stored.ID, PluginConfig: redirected.PluginConfig}); !isLaneRefusal(err) {
		t.Fatalf("options probe err = %v, want lane refusal", err)
	}
	// Turning an ordinary connection into an audiobook one is refused too.
	converted := routerInstOn("radarr-1", 1)
	converted.SupportedMediaTypes = []string{"audiobook"}
	if _, err := service.UpdateIntegration(context.Background(), admin, converted); !isLaneRefusal(err) {
		t.Fatalf("conversion err = %v, want lane refusal", err)
	}
	if router.validateCalls != 0 || router.gotOptionsConn.APIKey != "" {
		t.Fatalf("plugin reached: validate=%d options=%+v", router.validateCalls, router.gotOptionsConn)
	}
	if got := store.integrations[0]; got.PluginConfig["qbittorrent_url"] != "https://client.example.test" || got.APIKeyRef != "abk1.e30" {
		t.Fatalf("stored audiobook connection changed: %+v", got)
	}
}

func TestGenericEditsRefuseConnectionsOfTheAudiobookPlugin(t *testing.T) {
	base := newFakeStore()
	stored := audiobookLaneInst("abk-1", 7)
	stored.SupportedMediaTypes = []string{}
	base.integrations = []Integration{stored, routerInstOn("radarr-1", 1)}
	store := laneStore{fakeStore: base, plugins: map[int]string{1: "silo.requests.arr", 7: bloemAudiobookLanePluginID}}
	service := NewService(store, &fakeTMDBClient{}, &fakePresence{})
	service.SetRouterProvider(&fakeRouterProvider{})
	admin := Viewer{UserID: 1, IsAdmin: true}
	if _, err := service.UpdateIntegration(context.Background(), admin, stored); !isLaneRefusal(err) {
		t.Fatalf("stored plugin err = %v, want lane refusal", err)
	}
	moved := routerInstOn("radarr-1", 7)
	if _, err := service.UpdateIntegration(context.Background(), admin, moved); !isLaneRefusal(err) {
		t.Fatalf("installation move err = %v, want lane refusal", err)
	}
}

func TestBloemLaneWriterAndOrdinaryEditsPassTheGuard(t *testing.T) {
	store := newFakeStore()
	stored := audiobookLaneInst("abk-1", 7)
	store.integrations = []Integration{stored, routerInst("router-1")}
	router := &fakeRouterProvider{}
	service := newTestService(store)
	service.SetRouterProvider(router)
	admin := Viewer{UserID: 1, IsAdmin: true}

	if _, err := service.UpdateIntegration(WithBloemLaneIntegrationWriter(context.Background()), admin, stored); err != nil {
		t.Fatalf("lane writer update: %v", err)
	}
	if _, err := service.UpdateIntegrationConditional(WithBloemLaneIntegrationWriter(context.Background()), admin, stored, -1); isLaneRefusal(err) {
		t.Fatalf("lane writer conditional update refused: %v", err)
	}
	renamed := routerInst("router-1")
	renamed.Name = "renamed"
	if _, err := service.UpdateIntegration(context.Background(), admin, renamed); err != nil {
		t.Fatalf("ordinary update: %v", err)
	}
	if _, err := service.LoadIntegrationOptions(context.Background(), admin, Integration{ID: "new", APIKeyRef: "typed", InstallationID: stored.InstallationID, CapabilityID: "audiobooks", PluginConfig: stored.PluginConfig}); isLaneRefusal(err) {
		t.Fatalf("probe with typed credentials refused: %v", err)
	}
}
