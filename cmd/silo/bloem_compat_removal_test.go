package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api"
)

// Exercise the production API router and public listener over a real HTTP
// connection. The SPA may return its shell, but never a protocol/media response.
func TestBloemPublicServerHasNoCompatibilityEndpoints(t *testing.T) {
	const shell = "<!doctype html><title>Native application</title>"
	frontend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, shell)
	})
	server := httptest.NewServer(bloemRootHandler(api.NewRouter(api.Dependencies{}), frontend))
	defer server.Close()
	paths := []string{
		"/Users/AuthenticateByName", "/System/Info/Public", "/socket",
		"/Videos/retired/stream", "/Videos/retired/Attachments/0",
		"/Videos/retired/retired/Subtitles/0/Stream.srt", "/Videos/retired/Trickplay/320/0.jpg",
		"/audiobookshelf/api/login", "/audiobookshelf/api/items/retired/download",
		"/audiobookshelf/api/items/retired/file/retired", "/audiobookshelf/feed/retired",
		"/socket.io/?EIO=4&transport=polling", "/api/internal/compat/v1/applications",
		"/api/v1/compat/connect-info", "/api/v2/compat/connect-info",
	}
	for _, credential := range []string{"", "Bearer invented-native-fixture", "Bearer invented-retired-fixture"} {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
			for _, path := range paths {
				req, err := http.NewRequest(method, server.URL+path, strings.NewReader(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", credential)
				req.Header.Set("X-Emby-Token", "invented-retired-fixture")
				req.Header.Set("Range", "bytes=0-31")
				resp, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if resp.StatusCode == http.StatusPartialContent || resp.StatusCode == http.StatusSwitchingProtocols || resp.Header.Get("Content-Range") != "" {
					t.Fatalf("%s %s exposes protocol/media: %d", method, path, resp.StatusCode)
				}
				if strings.HasPrefix(path, "/api/") {
					if resp.StatusCode != http.StatusNotFound {
						t.Errorf("%s %s: status=%d body=%s", method, path, resp.StatusCode, body)
					}
				} else if resp.Header.Get("Content-Type") != "text/html" || (method != http.MethodHead && string(body) != shell) {
					t.Errorf("%s %s: unexpected protocol response %d %s", method, path, resp.StatusCode, body)
				}
			}
		}
	}
}
