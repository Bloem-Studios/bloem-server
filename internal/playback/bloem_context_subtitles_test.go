package playback

import (
	"context"
	"errors"
	"testing"
)

// S5: an embedded subtitle of a context-scoped input is extracted from the
// host's pinned source, never by opening the opaque path.
func TestExtractSubtitleUsesContextHost(t *testing.T) {
	refused := errors.New("source revoked")
	host := &fakeContextMediaHost{handles: contextMediaTestPath, err: refused}
	ctx := WithContextMediaHost(context.Background(), host)
	if _, err := ExtractSubtitleWithFormat(ctx, contextMediaTestPath, 0, "srt", "/nonexistent/ffmpeg"); !errors.Is(err, refused) || host.inputs != 1 {
		t.Fatalf("refused source: err=%v inputs=%d", err, host.inputs)
	}
	opened := errors.New("opened through the source")
	var openedPath, openedSession string
	host.err = nil
	host.input = TranscodeInputBinding{InputPath: contextMediaTestPath, InputFormat: "matroska", SessionID: "upstream", InputSource: &TranscodeInputSource{Open: func(_ context.Context, opts TranscodeOpts) (TranscodeInput, error) {
		openedPath, openedSession = opts.InputPath, opts.SessionID
		return nil, opened
	}}}
	if _, err := ExtractSubtitleWithFormat(ctx, contextMediaTestPath, 2, "ass", "/nonexistent/ffmpeg"); !errors.Is(err, opened) {
		t.Fatalf("source extract err = %v, want the source's open", err)
	}
	if openedPath != contextMediaTestPath || openedSession != "upstream" {
		t.Fatalf("source opened for %q in session %q", openedPath, openedSession)
	}
	if _, err := ExtractSubtitleWithFormat(ctx, contextMediaTestPath, 0, "vtt"); err == nil {
		t.Fatal("unsupported format reached the source")
	}
}

// A context-scoped input is never a file here, so the subtitle cache, which
// keys entries by the file's size and modification time, keeps nothing for it:
// a new revision can never be answered with an older revision's text.
func TestSubtitleCacheKeepsNoTextForContextInputs(t *testing.T) {
	dir := t.TempDir()
	cache := NewSubtitleCache(func() string { return dir })
	calls := 0
	extract := func(text string) func(context.Context) ([]byte, error) {
		return func(context.Context) ([]byte, error) {
			calls++
			return []byte(text), nil
		}
	}
	first, err := cache.ExtractText(t.Context(), contextMediaTestPath, 0, "srt", extract("revision one"))
	if err != nil || string(first) != "revision one" {
		t.Fatalf("first extract: %q %v", first, err)
	}
	second, err := cache.ExtractText(t.Context(), contextMediaTestPath, 0, "srt", extract("revision two"))
	if err != nil || string(second) != "revision two" || calls != 2 {
		t.Fatalf("second extract served %q after %d extracts; a cached entry outlived its revision", second, calls)
	}
}
