package abs

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/compatgateway"
	"github.com/Silo-Server/silo-server/internal/models"
)

func TestPublicMountResponseURLs(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		headers    map[string]string
		throughGW  bool
		verified   bool
		wantPrefix string
	}{
		{
			name:       "in-process audiobookshelf mount wins over forged plugin identity",
			path:       "/audiobookshelf/api/items/book-1",
			headers:    map[string]string{"X-Silo-User-Id": "forged"},
			throughGW:  true,
			wantPrefix: "http://media.example/audiobookshelf",
		},
		{
			name:       "standalone root ignores caller prefixes",
			path:       "/api/items/book-1",
			headers:    map[string]string{"X-Bloem-Public-Mount": "/audiobookshelf", "X-Forwarded-Prefix": "/forged", "Forwarded": "host=evil.example;proto=https"},
			wantPrefix: "http://media.example",
		},
		{
			name:       "gateway-verified companion mount wins over forged plugin identity",
			path:       "/api/items/book-1",
			headers:    map[string]string{"X-Bloem-Public-Mount": "/audiobookshelf", "X-Silo-User-Id": "forged"},
			verified:   true,
			wantPrefix: "http://media.example/audiobookshelf",
		},
		{
			name:       "host plugin mount",
			path:       "/api/items/book-1",
			headers:    map[string]string{"X-Silo-User-Id": "7", "X-Forwarded-Prefix": "/forged"},
			wantPrefix: "http://media.example/api/v1/plugins/install-7",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := New(Dependencies{
				MediaStore: noopMediaStore{},
				InstallID:  func() string { return "install-7" },
				InternalGatewayIdentityVerified: func(*http.Request) bool {
					return test.verified
				},
			})
			application := h.publicMountMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				baseURL := h.absBaseURL(r)
				item := &models.MediaItem{ContentID: "book-1", Title: "Book"}
				files := []*models.MediaFile{{FilePath: "/library/book-1.mp3", Duration: 60}}
				summary := siloItemToLibraryItem(item, AudiobookLibrary{ID: 1}, baseURL)
				detail := siloItemToLibraryItemDetail(item, files, AudiobookLibrary{ID: 1}, baseURL)
				playTracks := buildSiloAudioTracks("book-1", files, baseURL, "session-1")
				playItem := buildSiloPlayLibraryItem(item, "book-1", map[string]any{}, playTracks, nil, 60, baseURL)
				playMedia := playItem["media"].(map[string]any)
				_, _ = fmt.Fprintf(w, "%s\n%s\n%s\n%s\n",
					summary.Media.CoverPath,
					detail.Media.Tracks[0].ContentURL,
					playTracks[0].ContentURL,
					playMedia["coverPath"],
				)
			}))

			req := httptest.NewRequest(http.MethodGet, "http://media.example"+test.path, nil)
			for key, value := range test.headers {
				req.Header.Set(key, value)
			}
			rec := httptest.NewRecorder()
			if test.throughGW {
				gateway := compatgateway.New(compatgateway.Config{
					IdentitySecret: []byte("gateway-test-secret"),
					LocalHandlers:  map[compatgateway.AppKind]http.Handler{compatgateway.KindAudiobookshelf: application},
				})
				gateway.ServeHTTP(rec, req)
			} else {
				application.ServeHTTP(rec, req)
			}

			want := []string{
				test.wantPrefix + "/api/items/book-1/cover",
				test.wantPrefix + "/abs/api/items/book-1/file/212405636903327",
				test.wantPrefix + "/abs/public/session/session-1/track/1",
				test.wantPrefix + "/api/items/book-1/cover",
			}
			got := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("response URLs:\n got %q\nwant %q", got, want)
			}
			for _, value := range got {
				if strings.Count(value, test.wantPrefix) != 1 || strings.Contains(value, "/forged") || strings.Contains(value, "evil.example") {
					t.Fatalf("response URL did not join its trusted base exactly once: %q", value)
				}
			}
		})
	}
}

func TestPublicURLJoinsBaseExactlyOnce(t *testing.T) {
	tests := []struct {
		base string
		path string
		want string
	}{
		{base: "http://media.example", path: "/api/items/book-1/cover", want: "http://media.example/api/items/book-1/cover"},
		{base: "http://media.example/audiobookshelf/", path: "api/items/book-1/cover", want: "http://media.example/audiobookshelf/api/items/book-1/cover"},
		{base: "http://media.example/api/v1/plugins/install-7", path: "/public/session/s/track/1", want: "http://media.example/api/v1/plugins/install-7/public/session/s/track/1"},
	}
	for _, test := range tests {
		if got := publicURL(test.base, test.path); got != test.want {
			t.Errorf("publicURL(%q, %q) = %q, want %q", test.base, test.path, got, test.want)
		}
	}
}
