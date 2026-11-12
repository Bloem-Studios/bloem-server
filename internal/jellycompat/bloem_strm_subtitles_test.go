package jellycompat

// Bloem: subtitles of a `.strm` item for Jellyfin apps. A local `.strm`
// catalog file is a native location: Bloem's file guard
// (cmd/silo/bloem_compat.go) returns it only inside a stream request that
// carries the native media host (cmd/silo/bloem_compat_native.go). Inside one,
// Silo's subtitle route serves the subtitle files beside it as it does for any
// local file, and extracts an embedded track through the host's pinned
// source. ffmpeg and ffprobe never open the `.strm` file itself: its text is
// whatever anyone who can write to the library folder put there.

import (
	"bytes"
	"context"
	"errors"
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

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/procgroup"
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

// recordingMediaTools returns an ffmpeg path whose ffmpeg and ffprobe
// (playback derives ffprobe beside ffmpeg) log every argument to the
// returned file before running the real tools.
func recordingMediaTools(t *testing.T, ffmpeg string) (string, string) {
	t.Helper()
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe required")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "arguments.log")
	for name, tool := range map[string]string{"ffmpeg": ffmpeg, "ffprobe": ffprobe} {
		script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> '" + log + "'\nexec '" + tool + "' \"$@\"\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "ffmpeg"), log
}

func TestStrmSubtitlesServeForJellyfinApps(t *testing.T) {
	realFFmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required")
	}
	ffmpeg, toolLog := recordingMediaTools(t, realFFmpeg)
	font, err := os.ReadFile(filepath.Join("..", "..", "web", "public", "vendor", "pdfjs", "standard_fonts", "LiberationSans-Regular.ttf"))
	if err != nil {
		t.Fatal(err)
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
	if out, err := exec.Command(realFFmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=10", "-i", embedded,
		"-t", "2", "-map", "0:v", "-map", "1:s", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-c:s", "srt",
		"-attach", filepath.Join("..", "..", "web", "public", "vendor", "pdfjs", "standard_fonts", "LiberationSans-Regular.ttf"),
		"-metadata:s:t", "mimetype=application/x-truetype-font", media).CombinedOutput(); err != nil {
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
	store.Put(PlaybackSession{ID: "play-1", CompatToken: "token-1", RouteItemID: "item-1", ItemID: "movie-1", UpstreamSessionID: "upstream-1", UpstreamPlayMethod: "direct", MediaSources: []PlaybackMediaSource{source}})
	cacheDir := t.TempDir()
	handler := &PlaybackHandler{playbackStore: store, fileResolver: strmGuardedFiles{file: file}, FFmpegPath: ffmpeg,
		SubtitleCache: playback.NewSubtitleCache(func() string { return cacheDir }),
		content:       &stubContentService{detail: &upstreamItemDetail{ContentID: "movie-1", Versions: []catalog.FileVersion{{FileID: file.ID}}}}}
	host := &strmSubtitleHost{strm: strm, media: strmMediaURL(t, media)}

	serve := func(path string, index int, withHost bool, handle http.HandlerFunc) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path+"?PlaySessionId=play-1", nil)
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
		handle(w, r.WithContext(ctx))
		return w
	}
	request := func(index int, withHost bool) *httptest.ResponseRecorder {
		w := serve("/Videos/item-1/source-42/Subtitles/"+strconv.Itoa(index)+"/stream.srt", index, withHost, handler.HandleSubtitleStream)
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
	// Nothing is cached under the `.strm` file's size and time: the next
	// request reads the host's source again, which rechecks its authority.
	inputs := host.inputs
	if w := request(1, true); w.Code != http.StatusOK || host.inputs == inputs {
		t.Fatalf("second embedded request = %d, host inputs %d -> %d", w.Code, inputs, host.inputs)
	}
	if _, cached := handler.SubtitleCache.LookupText(strm, 0, "srt"); cached {
		t.Fatal("embedded text was cached under the .strm file")
	}

	// The attached font comes from the host's source too.
	attachment := func(withHost bool) *httptest.ResponseRecorder {
		return serve("/Videos/item-1/source-42/Attachments/2", 2, withHost, handler.HandleAttachment)
	}
	if w := attachment(true); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), font) {
		t.Fatalf("attached font = %d, %d bytes", w.Code, w.Body.Len())
	}
	hosted := playback.WithContextMediaHost(t.Context(), host)
	playback.NoteContextStream(hosted, playback.ContextStream{UserID: 7, PlaybackSessionID: "upstream-1", MediaFileID: file.ID})
	if fonts, err := playback.ListAttachedSubtitleFonts(hosted, strm, ffmpeg); err != nil || len(fonts) != 1 || fonts[0].Index != 2 || fonts[0].FileName != "LiberationSans-Regular.ttf" {
		t.Fatalf("listed fonts through the host = %+v, %v", fonts, err)
	}
	// Without the host there are no fonts, and ffprobe is not asked.
	if fonts, err := playback.ListAttachedSubtitleFonts(t.Context(), strm, ffmpeg); err != nil || fonts != nil {
		t.Fatalf("listed fonts without the host = %+v, %v", fonts, err)
	}
	if fonts, err := playback.ExtractAttachedSubtitleFonts(t.Context(), strm, ffmpeg); err != nil || fonts != nil {
		t.Fatalf("font bundle without the host = %d fonts, %v", len(fonts), err)
	}
	// Silo's batch demux and a direct extract refuse the file before ffmpeg runs.
	if err := handler.extractTextSubtitleBatch(t.Context(), strm, []playback.TextSubtitleOutput{{TextSubtitleTrack: playback.TextSubtitleTrack{Ordinal: 0, Format: "srt"}, Path: filepath.Join(t.TempDir(), "out.srt")}}); !errors.Is(err, procgroup.ErrNativeInput) {
		t.Fatalf("batch demux of the .strm file = %v, want ErrNativeInput", err)
	}
	if _, err := playback.ExtractSubtitleWithFormat(t.Context(), strm, 0, "srt", ffmpeg); !errors.Is(err, procgroup.ErrNativeInput) {
		t.Fatalf("extract without the host = %v, want ErrNativeInput", err)
	}

	// Outside a stream request with the host, the guard hides the file.
	for _, index := range []int{external, 1} {
		if w := request(index, false); w.Code != http.StatusNotFound {
			t.Fatalf("track %d without the native media host = %d, want 404", index, w.Code)
		}
	}
	if w := attachment(false); w.Code != http.StatusNotFound {
		t.Fatalf("attachment without the native media host = %d, want 404", w.Code)
	}

	// The tools ran, on the host's source only.
	arguments, err := os.ReadFile(toolLog)
	if err != nil || len(arguments) == 0 {
		t.Fatalf("media tools did not run: %v", err)
	}
	for _, argument := range strings.Split(string(arguments), "\n") {
		if strings.Contains(argument, ".strm") {
			t.Fatalf("a media tool was given the .strm file: %q", argument)
		}
	}
}
