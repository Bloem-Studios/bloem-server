package playback

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const contextMediaTestPath = "host-media:pinned-reference"

type fakeContextMediaHost struct {
	direct  int
	inputs  int
	input   TranscodeInputBinding
	err     error
	handles string
}

func (h *fakeContextMediaHost) Handles(path string) bool { return path == h.handles }

func (h *fakeContextMediaHost) ServeDirect(w http.ResponseWriter, _ *http.Request, _ string) error {
	h.direct++
	w.WriteHeader(http.StatusPartialContent)
	return nil
}

func (h *fakeContextMediaHost) Input(context.Context, string) (TranscodeInputBinding, error) {
	h.inputs++
	return h.input, h.err
}

type statusError int

func (e statusError) Error() string   { return "status" }
func (e statusError) HTTPStatus() int { return int(e) }

func TestContextStreamIsNotedOnlyWithAHost(t *testing.T) {
	stream := ContextStream{Credential: "secret", UserID: 7, ProfileID: "p", PlaybackSessionID: "s", MediaFileID: 3}
	plain := context.Background()
	NoteContextStream(plain, stream)
	if _, ok := ContextStreamFrom(plain); ok || HasContextMediaHost(plain) {
		t.Fatal("a request without a host recorded a stream")
	}
	ctx := WithContextMediaHost(context.Background(), &fakeContextMediaHost{})
	if _, ok := ContextStreamFrom(ctx); ok {
		t.Fatal("stream known before it was noted")
	}
	NoteContextStream(ctx, stream)
	if got, ok := ContextStreamFrom(ctx); !ok || got != stream {
		t.Fatalf("noted stream = %+v %v", got, ok)
	}
}

// S2: direct play hands a context-scoped input to the host and leaves every
// other path, and every request without a host, to the file server.
func TestServeDirectPlayUsesContextHostOnlyForItsInputs(t *testing.T) {
	host := &fakeContextMediaHost{handles: contextMediaTestPath}
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(WithContextMediaHost(context.Background(), host))
	response := httptest.NewRecorder()
	if err := ServeDirectPlay(response, request, contextMediaTestPath); err != nil || response.Code != http.StatusPartialContent || host.direct != 1 {
		t.Fatalf("host direct play: code=%d err=%v calls=%d", response.Code, err, host.direct)
	}
	response = httptest.NewRecorder()
	_ = ServeDirectPlay(response, request, "/nonexistent/other.mkv")
	if response.Code != http.StatusNotFound || host.direct != 1 {
		t.Fatalf("other path: code=%d calls=%d", response.Code, host.direct)
	}
	response = httptest.NewRecorder()
	_ = ServeDirectPlay(response, httptest.NewRequest(http.MethodGet, "/", nil), contextMediaTestPath)
	if response.Code != http.StatusNotFound || host.direct != 1 {
		t.Fatalf("no host: code=%d calls=%d", response.Code, host.direct)
	}
}

// S3: a refused source answers with the host's status and never starts
// ffmpeg on the opaque path.
func TestServeRemuxRefusedContextInputAnswersHostStatus(t *testing.T) {
	host := &fakeContextMediaHost{handles: contextMediaTestPath, err: statusError(http.StatusForbidden)}
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(WithContextMediaHost(context.Background(), host))
	response := httptest.NewRecorder()
	err := ServeRemuxWithOptions(response, request, contextMediaTestPath, "mp4", 0, false, -1, 0, RemuxServeOptions{FFmpegPath: "/nonexistent/ffmpeg"})
	if err == nil || response.Code != http.StatusForbidden || host.inputs != 1 {
		t.Fatalf("refused remux: code=%d err=%v inputs=%d", response.Code, err, host.inputs)
	}
	if strings.Contains(response.Body.String(), contextMediaTestPath) {
		t.Fatal("refusal leaked the input reference")
	}
}

// S4: a copy seek anchor for a context-scoped input comes from the host's
// source, never from probing the opaque path.
func TestResolveCopySeekAnchorUsesContextHost(t *testing.T) {
	refused := errors.New("source revoked")
	host := &fakeContextMediaHost{handles: contextMediaTestPath, err: refused}
	ctx := WithContextMediaHost(context.Background(), host)
	if _, _, err := ResolveCopySeekAnchor(ctx, "/nonexistent/ffmpeg", contextMediaTestPath, 30, 6); !errors.Is(err, refused) || host.inputs != 1 {
		t.Fatalf("anchor err=%v inputs=%d; want the host's refusal", err, host.inputs)
	}
	if _, _, err := ResolveCopySeekAnchor(ctx, "/nonexistent/ffmpeg", contextMediaTestPath, 0, 6); err != nil || host.inputs != 1 {
		t.Fatalf("zero seek consulted the host: err=%v inputs=%d", err, host.inputs)
	}
}

// Local HLS: StartTranscode binds a context-scoped input to the host's
// source for the same playback session, and refuses a mismatched one.
func TestBindTranscodeInputUsesContextHost(t *testing.T) {
	source := &TranscodeInputSource{Open: func(context.Context, TranscodeOpts) (TranscodeInput, error) { return nil, errors.New("unused") }}
	host := &fakeContextMediaHost{handles: contextMediaTestPath, input: TranscodeInputBinding{InputPath: contextMediaTestPath, InputFormat: "matroska", SessionID: "upstream", InputSource: source}}
	ctx := WithContextMediaHost(context.Background(), host)
	opts, err := bindTranscodeInput(ctx, TranscodeOpts{InputPath: contextMediaTestPath, SessionID: "upstream"})
	if err != nil || opts.InputSource != source || opts.InputFormat != "matroska" {
		t.Fatalf("bound opts = %+v, err = %v", opts, err)
	}
	if _, err := bindTranscodeInput(ctx, TranscodeOpts{InputPath: contextMediaTestPath, SessionID: "another"}); err == nil {
		t.Fatal("source bound to another playback session")
	}
	host.err = statusError(http.StatusForbidden)
	if _, err := bindTranscodeInput(ctx, TranscodeOpts{InputPath: contextMediaTestPath, SessionID: "upstream"}); err == nil {
		t.Fatal("refused source still started a transcode")
	}
	ordinary, err := bindTranscodeInput(ctx, TranscodeOpts{InputPath: "/media/movie.mkv", SessionID: "upstream"})
	if err != nil || ordinary.InputSource != nil {
		t.Fatalf("file input changed: %+v %v", ordinary, err)
	}
}
