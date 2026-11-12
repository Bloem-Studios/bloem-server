package jellycompat

// Bloem: subtitles of a `.strm` item for Jellyfin apps. A local `.strm`
// catalog file is a native location: Bloem's file guard
// (cmd/silo/bloem_compat.go) returns it only inside a stream request that
// carries the native media host (cmd/silo/bloem_compat_native.go). Inside one,
// Silo's subtitle route serves the subtitle files beside it as it does for any
// local file, and extracts an embedded track through the host's pinned
// source.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

// strmGuardedFiles is Bloem's native file guard as the Jellyfin handlers
// see it: a native file only inside a request with a context media host.
type strmGuardedFiles struct{ file *models.MediaFile }

func (r strmGuardedFiles) GetByID(ctx context.Context, _ int) (*models.MediaFile, error) {
	if !playback.HasContextMediaHost(ctx) {
		return nil, scanner.ErrFileNotFound
	}
	return r.file, nil
}

// strmSubtitleHost hands out the remote media as the pinned source of the
// `.strm` catalog path, the way Bloem's host offers ffmpeg its private
// address for the opened source.
type strmSubtitleHost struct {
	strm, media string
	inputs      int
	noted       playback.ContextStream
}

func (h *strmSubtitleHost) Handles(path string) bool { return path == h.strm }

func (h *strmSubtitleHost) ServeDirect(w http.ResponseWriter, _ *http.Request, _ string) error {
	w.WriteHeader(http.StatusTeapot)
	return nil
}

func (h *strmSubtitleHost) Input(ctx context.Context, path string) (playback.TranscodeInputBinding, error) {
	h.inputs++
	h.noted, _ = playback.ContextStreamFrom(ctx)
	media := h.media
	return playback.TranscodeInputBinding{InputPath: path, InputFormat: "matroska", SessionID: "upstream-1", InputSource: &playback.TranscodeInputSource{
		Open: func(context.Context, playback.TranscodeOpts) (playback.TranscodeInput, error) {
			return strmSourceInput(media), nil
		},
	}}, nil
}

type strmSourceInput string

func (i strmSourceInput) Run(consume func(context.Context, string) error) error {
	return consume(context.Background(), string(i))
}

func (strmSourceInput) Close() error { return nil }

// strmMediaURL serves path over HTTP, as the host's private source address.
func strmMediaURL(t *testing.T, path string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, path) }))
	t.Cleanup(server.Close)
	return server.URL + "/source"
}

func TestStrmSubtitlesServeForJellyfinApps(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required")
	}
	dir := filepath.Join(t.TempDir(), "Harbor Lights (2019)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	strm := filepath.Join(dir, "Harbor Lights (2019).strm")
	if err := os.WriteFile(strm, []byte("https://media.example.net/films/harbor.mkv?token=very-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, "Harbor Lights (2019).en.srt")
	if err := os.WriteFile(sidecar, []byte("1\n00:00:00,500 --> 00:00:01,500\nFrom the sidecar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	embedded := filepath.Join(t.TempDir(), "embedded.srt")
	if err := os.WriteFile(embedded, []byte("1\n00:00:00,500 --> 00:00:01,500\nFrom the remote video\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(t.TempDir(), "remote.mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=10", "-i", embedded,
		"-t", "2", "-map", "0:v", "-map", "1:s", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-c:s", "srt", media).CombinedOutput(); err != nil {
		t.Fatalf("synthetic media: %v %s", err, out)
	}
	if !scanner.StrmSidecarSubtitle(strm, sidecar) {
		t.Fatal("Silo's sidecar rules do not give the subtitle file to the .strm file")
	}

	file := &models.MediaFile{
		ID: 42, ContentID: "movie-1", FilePath: strm, Container: "mkv",
		VideoTracks:       []models.VideoTrack{{Codec: "h264", Width: 64, Height: 48}},
		SubtitleTracks:    []models.SubtitleTrack{{Index: 1, Codec: "subrip", Language: "eng"}},
		ExternalSubtitles: []models.ExternalSubtitle{{Path: sidecar, Language: "eng", Format: "srt"}},
	}
	source := PlaybackMediaSource{ID: "source-42", FileID: file.ID}
	store := NewPlaybackSessionStore(time.Hour, nil)
	store.Put(PlaybackSession{ID: "play-1", CompatToken: "token-1", RouteItemID: "item-1", UpstreamSessionID: "upstream-1", UpstreamPlayMethod: "direct", MediaSources: []PlaybackMediaSource{source}})
	cacheDir := t.TempDir()
	handler := &PlaybackHandler{playbackStore: store, fileResolver: strmGuardedFiles{file: file}, FFmpegPath: ffmpeg,
		SubtitleCache: playback.NewSubtitleCache(func() string { return cacheDir })}
	host := &strmSubtitleHost{strm: strm, media: strmMediaURL(t, media)}

	request := func(index int, withHost bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/Videos/item-1/source-42/Subtitles/"+strconv.Itoa(index)+"/stream.srt?PlaySessionId=play-1", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("routeItemId", "item-1")
		routeCtx.URLParams.Add("routeMediaSourceId", source.ID)
		routeCtx.URLParams.Add("routeIndex", strconv.Itoa(index))
		routeCtx.URLParams.Add("routeFormat", "srt")
		ctx := context.WithValue(t.Context(), chi.RouteCtxKey, routeCtx)
		ctx = context.WithValue(ctx, compatSessionKey, &Session{Token: "token-1", StreamAppUserID: 7, ProfileID: "profile-1"})
		if withHost {
			ctx = playback.WithContextMediaHost(ctx, host)
		}
		w := httptest.NewRecorder()
		handler.HandleSubtitleStream(w, r.WithContext(ctx))
		if strings.Contains(w.Body.String(), "very-secret") || strings.Contains(w.Body.String(), "media.example.net") {
			t.Fatalf("track %d names the .strm address: %q", index, w.Body.String())
		}
		return w
	}

	external := externalSubtitleRouteIndex(file, 0)
	if w := request(external, true); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "From the sidecar") {
		t.Fatalf("sidecar = %d %q", w.Code, w.Body.String())
	}
	if w := request(1, true); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "From the remote video") {
		t.Fatalf("embedded track = %d %q", w.Code, w.Body.String())
	}
	if host.inputs == 0 || host.noted.PlaybackSessionID != "upstream-1" || host.noted.MediaFileID != file.ID || host.noted.UserID != 7 {
		t.Fatalf("embedded track did not read the host's source for the noted stream: inputs=%d stream=%+v", host.inputs, host.noted)
	}
	// Outside a stream request with the host, the guard hides the file.
	for _, index := range []int{external, 1} {
		if w := request(index, false); w.Code != http.StatusNotFound {
			t.Fatalf("track %d without the native media host = %d, want 404", index, w.Code)
		}
	}
}
