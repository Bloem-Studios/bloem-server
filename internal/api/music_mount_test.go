package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/go-chi/chi/v5"
)

func TestMusicRoutesMountOnlyOnNativeV2(t *testing.T) {
	pool := newDisposableAPIDatabase(t, "bloem_music_routes_", false)
	router := NewRouter(Dependencies{
		DB:                pool,
		FileRepo:          scanner.NewFileRepository(pool),
		UserStoreProvider: pgstore.NewPostgresProvider(pool),
		Config: &config.Config{Auth: config.AuthConfig{
			JWTSecret:          "music-route-secret",
			AccessTokenExpiry:  time.Hour,
			RefreshTokenExpiry: time.Hour,
		}},
	})
	want := map[string]bool{
		"GET /api/v2/music/status":       false,
		"GET /api/v2/music/artists":      false,
		"GET /api/v2/music/artists/{id}": false,
		"GET /api/v2/music/albums/{id}":  false,
	}
	if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if _, ok := want[key]; ok {
			want[key] = true
		}
		if len(route) >= len("/api/v1/music") && route[:len("/api/v1/music")] == "/api/v1/music" {
			t.Fatalf("native music route leaked into Silo-compatible v1: %s", key)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for route, mounted := range want {
		if !mounted {
			t.Errorf("route is not mounted: %s", route)
		}
	}
}
