package playback

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/Silo-Server/silo-server/internal/procgroup"
)

// Context-scoped media sources serve inputs that are not files on this host,
// such as a storage plugin's catalog reference. A request that may play one
// carries a ContextMediaHost; the serving helpers (ServeDirectPlay,
// ServeRemuxWithOptions, ResolveCopySeekAnchor, StartTranscode) hand such an
// input to the host instead of opening a path. Without a host, or for a path
// the host does not handle, they behave exactly as before, and an input that
// is not a file fails as a missing file would.

// ContextStream identifies the playback a request serves: the caller's
// credential (request memory only, never stored), its account and profile,
// the playback session and the media file. A protocol front end notes it
// once it knows them (NoteContextStream); the host authorizes from it.
type ContextStream struct {
	Credential        string
	UserID            int
	ProfileID         string
	PlaybackSessionID string
	MediaFileID       int
}

// ContextMediaHost resolves context-scoped inputs. Handles reports whether an
// input path is one of the host's. ServeDirect answers a direct-play request
// itself, including every error response. Input returns a revision-pinned
// source for the request's stream, bound to its playback session.
type ContextMediaHost interface {
	Handles(inputPath string) bool
	ServeDirect(w http.ResponseWriter, r *http.Request, inputPath string) error
	Input(ctx context.Context, inputPath string) (TranscodeInputBinding, error)
}

type contextMediaKey struct{}

type contextMedia struct {
	host ContextMediaHost

	mu     sync.Mutex
	stream ContextStream
	noted  bool
}

// WithContextMediaHost scopes host to ctx's request. Its streams start unknown.
func WithContextMediaHost(ctx context.Context, host ContextMediaHost) context.Context {
	if host == nil {
		return ctx
	}
	return context.WithValue(ctx, contextMediaKey{}, &contextMedia{host: host})
}

// HasContextMediaHost reports whether ctx's request may play context-scoped
// inputs.
func HasContextMediaHost(ctx context.Context) bool {
	media, _ := ctx.Value(contextMediaKey{}).(*contextMedia)
	return media != nil
}

// NoteContextStream records the playback ctx's request serves. It does
// nothing outside a request with a ContextMediaHost.
func NoteContextStream(ctx context.Context, stream ContextStream) {
	media, _ := ctx.Value(contextMediaKey{}).(*contextMedia)
	if media == nil {
		return
	}
	media.mu.Lock()
	media.stream, media.noted = stream, true
	media.mu.Unlock()
}

// ContextStreamFrom returns the playback ctx's request serves, once noted.
func ContextStreamFrom(ctx context.Context) (ContextStream, bool) {
	media, _ := ctx.Value(contextMediaKey{}).(*contextMedia)
	if media == nil {
		return ContextStream{}, false
	}
	media.mu.Lock()
	defer media.mu.Unlock()
	return media.stream, media.noted
}

func contextMediaHost(ctx context.Context, inputPath string) ContextMediaHost {
	media, _ := ctx.Value(contextMediaKey{}).(*contextMedia)
	if media == nil || !media.host.Handles(inputPath) {
		return nil
	}
	return media.host
}

// serveContextDirectPlay is ServeDirectPlay's hook for context-scoped inputs.
func serveContextDirectPlay(w http.ResponseWriter, r *http.Request, inputPath string) (bool, error) {
	host := contextMediaHost(r.Context(), inputPath)
	if host == nil {
		return false, nil
	}
	return true, host.ServeDirect(w, r, inputPath)
}

// ContextRemux is one progressive remux request, as ServeRemuxWithOptions
// receives it.
type ContextRemux struct {
	InputPath       string
	OutputFormat    string
	SeekSeconds     float64
	TranscodeAudio  bool
	AudioTrackIndex int
	DVProfile       int
	Options         RemuxServeOptions
}

// serveContextRemux is ServeRemuxWithOptions' hook for context-scoped inputs:
// it remuxes the host's pinned source through the supervised input bridge.
func serveContextRemux(w http.ResponseWriter, r *http.Request, remux ContextRemux) (bool, error) {
	host := contextMediaHost(r.Context(), remux.InputPath)
	if host == nil {
		return false, nil
	}
	binding, err := host.Input(r.Context(), remux.InputPath)
	if err != nil {
		writeContextMediaError(w, err)
		return true, err
	}
	tracked := &contextMediaWriter{ResponseWriter: w}
	ffmpeg := remux.Options.FFmpegPath
	err = ServeSourceRemux(tracked, r, TranscodeOpts{
		InputPath: binding.InputPath, InputSource: binding.InputSource, InputFormat: binding.InputFormat,
		SessionID: binding.SessionID, FFmpegPath: ffmpeg, AudioTrackIndex: remux.AudioTrackIndex, SeekSeconds: remux.SeekSeconds,
	}, remux.OutputFormat, remux.TranscodeAudio, remux.DVProfile, remux.Options)
	if err != nil && !tracked.wrote {
		writeContextMediaError(w, err)
	}
	return true, err
}

// resolveContextCopySeekAnchor is ResolveCopySeekAnchor's hook for
// context-scoped inputs.
func resolveContextCopySeekAnchor(ctx context.Context, ffmpegPath, inputPath string, requested float64, segmentDuration int) (bool, float64, int, error) {
	host := contextMediaHost(ctx, inputPath)
	if host == nil {
		return false, 0, 0, nil
	}
	binding, err := host.Input(ctx, inputPath)
	if err != nil {
		return true, 0, 0, err
	}
	origin, segment, err := ResolveSourceCopySeekAnchor(ctx, ffmpegPath, inputPath, binding.InputSource, binding.InputFormat, requested, segmentDuration)
	return true, origin, segment, err
}

// extractContextSubtitle is ExtractSubtitleWithFormat's hook for
// context-scoped inputs: the track is extracted from the host's pinned source.
// Such an input is never a file here, so the subtitle cache, keyed by a
// file's size and modification time, keeps nothing for it and cannot serve
// one revision's text for another.
func extractContextSubtitle(ctx context.Context, inputPath string, trackIndex int, format, ffmpegPath string) (bool, []byte, error) {
	host := contextMediaHost(ctx, inputPath)
	if host == nil {
		return false, nil, nil
	}
	binding, err := host.Input(ctx, inputPath)
	if err != nil {
		return true, nil, err
	}
	data, err := ExtractSourceSubtitle(ctx, TranscodeOpts{
		InputPath: binding.InputPath, InputSource: binding.InputSource, InputFormat: binding.InputFormat,
		SessionID: binding.SessionID, FFmpegPath: ffmpegPath,
	}, trackIndex, format, 0, 0)
	return true, data, err
}

// listContextSubtitleFonts is ListAttachedSubtitleFonts' hook: a
// context-scoped input's fonts are probed from its host's pinned source. A
// native catalog location outside such a request has none listed: its path is
// never handed to ffprobe (a `.strm` file is a text file whose content anyone
// who can write to the library folder controls).
func listContextSubtitleFonts(ctx context.Context, inputPath, ffmpegPath string) (bool, []SubtitleFontMetadata, error) {
	opts, handled, err := contextFontSource(ctx, inputPath, ffmpegPath)
	if !handled || err != nil || opts.InputSource == nil {
		return handled, nil, err
	}
	streams, err := probeSourceFontStreams(ctx, opts)
	if err != nil {
		return true, nil, err
	}
	fonts := make([]SubtitleFontMetadata, 0, len(streams))
	for i, stream := range streams {
		name := safeAttachmentDisplayName(stream, fmt.Sprintf("attachment-%d%s", i, fontAttachmentExt(stream)))
		fonts = append(fonts, SubtitleFontMetadata{Index: stream.Index, Codec: stream.CodecName, FileName: name, MimeType: SubtitleFontMIMEType(name)})
	}
	return true, fonts, nil
}

// extractContextSubtitleFonts is extractAttachedSubtitleFonts' hook: a
// context-scoped input's fonts come from its host's pinned source, and a
// native catalog location outside such a request has none. index, when set,
// selects one font by its container stream index.
func extractContextSubtitleFonts(ctx context.Context, inputPath, ffmpegPath string, index *int) (bool, []SubtitleFontAttachment, error) {
	opts, handled, err := contextFontSource(ctx, inputPath, ffmpegPath)
	if !handled || err != nil || opts.InputSource == nil {
		return handled, nil, err
	}
	fonts, err := ExtractSourceSubtitleFonts(ctx, opts)
	if err != nil || index == nil {
		return true, fonts, err
	}
	for _, font := range fonts {
		if font.StreamIndex == *index {
			return true, []SubtitleFontAttachment{font}, nil
		}
	}
	return true, nil, nil
}

// contextFontSource resolves the font hooks' input: the host's pinned source
// for a context-scoped input (handled, with InputSource set), or handled with
// no source for a native catalog location without a host. Any other input is
// not handled and keeps Silo's file path.
func contextFontSource(ctx context.Context, inputPath, ffmpegPath string) (TranscodeOpts, bool, error) {
	host := contextMediaHost(ctx, inputPath)
	if host == nil {
		return TranscodeOpts{}, procgroup.NativeLocation(inputPath), nil
	}
	binding, err := host.Input(ctx, inputPath)
	if err != nil {
		return TranscodeOpts{}, true, err
	}
	return TranscodeOpts{
		InputPath: binding.InputPath, InputSource: binding.InputSource, InputFormat: binding.InputFormat,
		SessionID: binding.SessionID, FFmpegPath: ffmpegPath,
	}, true, nil
}

// errNativeSubtitleSource keeps a native catalog location out of the
// subtitle cache.
var errNativeSubtitleSource = errors.New("native catalog location is not cached")

// uncachedNativeSource is the subtitle cache's hook on its source stat. The
// cache keys an entry by the source file's size and modification time, and a
// hit is served without opening the source. For a `.strm` file those are the
// text file's, and a hit would skip the host's authority check on the
// remote source, so a native catalog location is never cached, as a storage
// identity (never a file here) already is not. Without a cache entry a text
// track is extracted on its own through the host, never by the batch demux
// that would hand ffmpeg the file.
func uncachedNativeSource(inputPath string, statErr error) error {
	if statErr == nil && procgroup.NativeLocation(inputPath) {
		return errNativeSubtitleSource
	}
	return statErr
}

// contextTranscodeInput gives StartTranscode the host's pinned source for a
// context-scoped input that no explicit binding covers.
func contextTranscodeInput(ctx context.Context, opts TranscodeOpts) (TranscodeOpts, error) {
	host := contextMediaHost(ctx, opts.InputPath)
	if host == nil || opts.InputSource != nil {
		return opts, nil
	}
	binding, err := host.Input(ctx, opts.InputPath)
	if err != nil {
		return opts, err
	}
	if binding.InputPath != opts.InputPath || binding.SessionID != opts.SessionID || binding.InputSource == nil || binding.InputSource.Open == nil {
		return opts, errors.New("source input does not match execution identity")
	}
	opts.InputSource, opts.InputFormat = binding.InputSource, binding.InputFormat
	return opts, nil
}

// ContextWorkerStartSealer is an optional ContextMediaHost capability: it
// gives a transcode worker the authority to read a context-scoped input
// before the start request (its exact JSON body) is sent to nodeURL.
type ContextWorkerStartSealer interface {
	SealWorkerStart(ctx context.Context, nodeURL string, start []byte) error
}

// SealContextWorkerStart is the remote transcode start's hook: within a
// request whose host can seal worker starts, it does so before the start is
// sent. Elsewhere it does nothing, and a worker refuses an input it has no
// authority for.
func SealContextWorkerStart(ctx context.Context, nodeURL string, start []byte) error {
	media, _ := ctx.Value(contextMediaKey{}).(*contextMedia)
	if media == nil {
		return nil
	}
	sealer, ok := media.host.(ContextWorkerStartSealer)
	if !ok {
		return nil
	}
	return sealer.SealWorkerStart(ctx, nodeURL, start)
}

// ContextMediaStatus maps a host error to a response status. A host error
// may carry its own (HTTPStatus() int).
func ContextMediaStatus(err error) int {
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) && status.HTTPStatus() >= 400 {
		return status.HTTPStatus()
	}
	if errors.Is(err, context.Canceled) {
		return 499
	}
	return http.StatusServiceUnavailable
}

func writeContextMediaError(w http.ResponseWriter, err error) {
	status := ContextMediaStatus(err)
	if status == 499 {
		return
	}
	http.Error(w, http.StatusText(status), status)
}

// contextMediaWriter records whether a response has started.
type contextMediaWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *contextMediaWriter) WriteHeader(status int) {
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *contextMediaWriter) Write(p []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(p)
}

func (w *contextMediaWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		w.wrote = true
		flusher.Flush()
	}
}

func (w *contextMediaWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
