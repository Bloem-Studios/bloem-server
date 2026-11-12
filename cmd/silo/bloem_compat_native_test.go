package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

type readyCompatHost struct{ ready bool }

func (h readyCompatHost) Ready() bool       { return h.ready }
func (readyCompatHost) Handles(string) bool { return true }
func (readyCompatHost) ServeDirect(http.ResponseWriter, *http.Request, string) error {
	return nil
}
func (readyCompatHost) Input(context.Context, string) (playback.TranscodeInputBinding, error) {
	return playback.TranscodeInputBinding{}, errors.New("unused")
}

// Only Jellyfin stream routes may reach native media; every other route keeps
// the native-file guard.
func TestCompatNativeMediaScopesHostToStreamRoutes(t *testing.T) {
	for path, want := range map[string]bool{
		"/Videos/abc/stream":                       true,
		"/videos/abc/stream.mkv":                   true,
		"/Videos/abc/stream?static=true":           true,
		"/Videos/abc/master.m3u8":                  true,
		"/videos/abc/main.m3u8":                    true,
		"/Videos/abc/hls1/main/0.mp4":              true,
		"/Videos/abc/def/Subtitles/2/0/Stream.srt": true,
		"/Videos/abc/def/Attachments/1":            true,
		"/Items/abc":                               false,
		"/Items/abc/Download":                      false,
		"/Users/Me":                                false,
		"/Videos/abc/Trickplay/320/0.jpg":          false,
		"/Audio/abc/stream":                        false,
		"/Items/abc/PlaybackInfo":                  false,
		"/Videos/ActiveEncodings":                  false,
	} {
		var saw bool
		handler := bloemCompatNativeMedia(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			saw = playback.HasContextMediaHost(r.Context())
		}), readyCompatHost{ready: true})
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		if saw != want {
			t.Errorf("%s: host installed = %v, want %v", path, saw, want)
		}
	}
	var saw bool
	handler := bloemCompatNativeMedia(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		saw = playback.HasContextMediaHost(r.Context())
	}), readyCompatHost{ready: false})
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/Videos/abc/stream", nil))
	if saw {
		t.Fatal("host installed before native delivery was ready")
	}
}

type compatFileFixture struct{ file *models.MediaFile }

func (f compatFileFixture) GetByID(context.Context, int) (*models.MediaFile, error) {
	return f.file, nil
}

// The guard returns a native file only within a request that may play it;
// marker population, trickplay and every other caller keep the 404.
func TestCompatNativeFileGuardOpensOnlyForStreamRequests(t *testing.T) {
	location, err := storagesource.CatalogLocation(uuid.New(), "movie")
	if err != nil {
		t.Fatal(err)
	}
	native := bloemGuardNativeStorageFiles(compatFileFixture{file: &models.MediaFile{ID: 1, FilePath: location}})
	if _, err := native.GetByID(t.Context(), 1); !errors.Is(err, scanner.ErrFileNotFound) {
		t.Fatalf("native file outside a stream request: %v", err)
	}
	streamCtx := playback.WithContextMediaHost(t.Context(), readyCompatHost{ready: true})
	if file, err := native.GetByID(streamCtx, 1); err != nil || file == nil || file.FilePath != location {
		t.Fatalf("native file in a stream request: %v %v", file, err)
	}
	ordinary := bloemGuardNativeStorageFiles(compatFileFixture{file: &models.MediaFile{ID: 2, FilePath: "/media/movie.mkv"}})
	if file, err := ordinary.GetByID(t.Context(), 2); err != nil || file == nil {
		t.Fatalf("ordinary file: %v %v", file, err)
	}
}

// main.go wraps the Jellyfin listener and gives the router the host.
func TestCompatNativeMediaIsWiredInMain(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, line := range []string{
		"deps.NativeVideoCompat = api.NewNativeVideoCompat()",
		"compatSrv.Handler = bloemCompatNativeMediaListener(compatSrv.Handler, deps.NativeVideoCompat, compat.SessionStore(), deps.DB)",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("main.go lacks %q", line)
		}
	}
}
