package playback

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// A protected MPEG-TS input is fetched by the server's authenticated, guarded
// HTTP client. FFmpeg sees only pipe:0, never provider credentials, redirects,
// or a playlist that can cause it to open additional network destinations.
func bloemLiveInputArgs(inputURL string, protected bool) []string {
	if protected {
		return []string{"-protocol_whitelist", "pipe", "-f", "mpegts", "-i", "pipe:0"}
	}
	return []string{"-i", inputURL}
}

func bloemOpenLiveInput(ctx context.Context, cmd *exec.Cmd, open func(context.Context) (io.ReadCloser, error)) (func() error, error) {
	if open == nil {
		return func() error { return nil }, nil
	}
	body, err := open(ctx)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("live MPEG-TS input unavailable")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = body.Close()
		return nil, errors.New("live MPEG-TS input pipe unavailable")
	}
	pipeReader, _ := cmd.Stdin.(io.Closer)
	var once sync.Once
	var closing atomic.Bool
	closeBody := func() {
		once.Do(func() {
			closing.Store(true)
			_ = stdin.Close()
			_ = body.Close()
		})
	}
	stop := context.AfterFunc(ctx, closeBody)
	cmd.WaitDelay = 5 * time.Second
	done := make(chan struct{})
	var copyErr error
	go func() {
		defer close(done)
		_, err := io.Copy(stdin, body)
		if err != nil && !closing.Load() && !errors.Is(err, syscall.EPIPE) {
			copyErr = errors.New("live MPEG-TS input ended unexpectedly")
		}
		_ = stdin.Close()
	}()
	// Own the copier instead of assigning a Reader to cmd.Stdin: os/exec must
	// be able to reap an exited encoder while an upstream Read is still blocked.
	// Call after Wait (or startup failure), then wait for our copier to end.
	return func() error {
		stop()
		closeBody()
		// Only the caller may close the child end, after Start has returned
		// (or before Start on a setup error). Cancellation can race fork/exec.
		if pipeReader != nil {
			_ = pipeReader.Close()
		}
		<-done
		return copyErr
	}, nil
}
