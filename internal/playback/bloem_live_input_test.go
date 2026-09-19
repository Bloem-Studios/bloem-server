package playback

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type bloemBlockedLiveInput struct {
	started, closed      chan struct{}
	startOnce, closeOnce sync.Once
	closes               atomic.Int32
}

func newBloemBlockedLiveInput() *bloemBlockedLiveInput {
	return &bloemBlockedLiveInput{started: make(chan struct{}), closed: make(chan struct{})}
}
func (b *bloemBlockedLiveInput) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, io.EOF
}
func (b *bloemBlockedLiveInput) Close() error {
	b.closes.Add(1)
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}
func bloemAwaitLive(t *testing.T, event <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func bloemAwaitLiveFile(t *testing.T, path string) {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("fixture encoder did not publish its state")
		}
	}
}

// Fixtures exercise process/input ownership, not decoding or physical playback.
func bloemLiveEncoderFixture(t *testing.T, script string) string {
	t.Helper()
	return writeLiveHLSFakeFFmpeg(t, `#!/bin/sh
last=""
for arg in "$@"; do last="$arg"; done
outdir=$(dirname "$last")
mkdir -p "$outdir"
printf '%s\n' "$@" > "$outdir/argv"
: > "$outdir/argv-ready"
`+script)
}

func TestBloemXtreamInputReapsExitedEncoderBeforeClosingBlockedReader(t *testing.T) {
	dir := t.TempDir()
	bin := bloemLiveEncoderFixture(t, `while [ ! -f "$outdir/exit" ]; do sleep 0.01; done
exit 0
`)
	body := newBloemBlockedLiveInput()
	cmd := exec.CommandContext(t.Context(), bin, filepath.Join(dir, "output.ts"))
	closeInput, err := bloemOpenLiveInput(t.Context(), cmd, func(context.Context) (io.ReadCloser, error) { return body, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeInput() }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	bloemAwaitLive(t, body.started, "blocked source read")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := os.WriteFile(filepath.Join(dir, "exit"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait remained coupled to the blocked upstream reader")
	}
	if body.closes.Load() != 0 {
		t.Fatal("test did not reap before input cleanup")
	}
	if err := closeInput(); err != nil {
		t.Fatal(err)
	}
	if body.closes.Load() != 1 {
		t.Fatal("protected input did not close exactly once")
	}
}

func TestBloemXtreamProtectedRecordLifecycle(t *testing.T) {
	for _, natural := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit close", true: "natural exit"}[natural], func(t *testing.T) {
			dir := t.TempDir()
			body := newBloemBlockedLiveInput()
			bin := bloemLiveEncoderFixture(t, `while [ ! -f "$outdir/exit" ]; do sleep 0.01; done
exit 0
`)
			session, err := StartLiveRecord(t.Context(), LiveRecordOpts{ID: "protected-record", InputURL: "https://fixture-private-user:fixture-private-password@provider.invalid/live", OpenMPEGTS: func(context.Context) (io.ReadCloser, error) { return body, nil }, OutputPath: filepath.Join(dir, "output.ts"), FFmpegPath: bin})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			bloemAwaitLive(t, body.started, "record input")
			bloemAwaitLiveFile(t, filepath.Join(dir, "argv-ready"))
			if natural {
				if err := os.WriteFile(filepath.Join(dir, "exit"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				bloemAwaitLive(t, session.Done(), "natural recorder exit")
			} else {
				if err := session.Close(); err != nil {
					t.Fatal(err)
				}
			}
			bloemAwaitLive(t, body.closed, "record input cleanup")
			if body.closes.Load() != 1 {
				t.Fatal("record input closed more than once")
			}
			args, err := os.ReadFile(filepath.Join(dir, "argv"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(args), "fixture-private") || !strings.Contains(string(args), "-protocol_whitelist\npipe\n-f\nmpegts\n-i\npipe:0\n") {
				t.Fatal("protected recorder exposed an upstream URL or lost pipe-only demuxing")
			}
		})
	}
}

func TestBloemXtreamProtectedHLSReadinessAndLifetime(t *testing.T) {
	body := newBloemBlockedLiveInput()
	bin := bloemLiveEncoderFixture(t, `printf 'fixture' > "$outdir/seg_00000.ts"
printf '#EXTM3U\n#EXTINF:1.0,\nseg_00000.ts\n' > "$outdir/index.m3u8"
cat >/dev/null
`)
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	var inputContext context.Context
	session, err := StartLiveHLS(parent, LiveHLSOpts{ID: "protected-hls", InputURL: "https://fixture-private-password@provider.invalid/live", OpenMPEGTS: func(ctx context.Context) (io.ReadCloser, error) { inputContext = ctx; return body, nil }, OutputDir: t.TempDir(), FFmpegPath: bin, HWAccel: "none", HWDecode: "off"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	cancel()
	if inputContext.Err() != nil {
		t.Fatal("successful HLS admission retained request cancellation")
	}
	select {
	case <-session.Done():
		t.Fatal("ready HLS encoder ended with its initiating request")
	case <-body.closed:
		t.Fatal("ready HLS input closed with its initiating request")
	case <-time.After(200 * time.Millisecond): // bounded observation of unwanted cancellation
	}
	args, err := os.ReadFile(filepath.Join(session.OutputDir, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "fixture-private") || !strings.Contains(string(args), "-protocol_whitelist\npipe\n-f\nmpegts\n-i\npipe:0\n") {
		t.Fatal("protected HLS exposed an upstream URL or lost pipe-only demuxing")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	bloemAwaitLive(t, body.closed, "HLS input cleanup")
	if body.closes.Load() != 1 {
		t.Fatal("HLS input was not closed exactly once")
	}
}

func TestBloemXtreamProtectedHLSCancellationDuringAdmission(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	dir := t.TempDir()
	go func() {
		_, err := StartLiveHLS(parent, LiveHLSOpts{ID: "cancel-admission", OutputDir: dir, FFmpegPath: "/does-not-exist/fixture-ffmpeg", HWAccel: "none", OpenMPEGTS: func(ctx context.Context) (io.ReadCloser, error) { close(entered); <-ctx.Done(); return nil, ctx.Err() }})
		done <- err
	}()
	bloemAwaitLive(t, entered, "protected admission")
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("admission cancellation = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled input admission remained blocked")
	}
}

func TestBloemXtreamProtectedHLSCancellationBeforeReadiness(t *testing.T) {
	body := newBloemBlockedLiveInput()
	bin := bloemLiveEncoderFixture(t, "cat >/dev/null\n")
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := StartLiveHLS(parent, LiveHLSOpts{ID: "cancel-readiness", OutputDir: dir, FFmpegPath: bin, HWAccel: "none", OpenMPEGTS: func(context.Context) (io.ReadCloser, error) { return body, nil }})
		done <- err
	}()
	bloemAwaitLive(t, body.started, "HLS stdin read")
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled HLS startup succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled readiness left an encoder/input running")
	}
	bloemAwaitLive(t, body.closed, "cancelled HLS input cleanup")
	if body.closes.Load() != 1 {
		t.Fatal("cancelled HLS input cleanup was not exactly once")
	}
}

func TestBloemXtreamProtectedInputStartFailuresCloseSource(t *testing.T) {
	for _, hls := range []bool{false, true} {
		t.Run(map[bool]string{false: "record", true: "HLS"}[hls], func(t *testing.T) {
			body := newBloemBlockedLiveInput()
			open := func(context.Context) (io.ReadCloser, error) { return body, nil }
			var err error
			if hls {
				_, err = StartLiveHLS(t.Context(), LiveHLSOpts{ID: "missing-binary", OutputDir: t.TempDir(), FFmpegPath: "/does-not-exist/fixture-ffmpeg", OpenMPEGTS: open, HWAccel: "none"})
			} else {
				_, err = StartLiveRecord(t.Context(), LiveRecordOpts{ID: "missing-binary", OutputPath: filepath.Join(t.TempDir(), "output.ts"), FFmpegPath: "/does-not-exist/fixture-ffmpeg", OpenMPEGTS: open})
			}
			if err == nil {
				t.Fatal("missing fixture binary started")
			}
			bloemAwaitLive(t, body.closed, "startup failure cleanup")
			if body.closes.Load() != 1 {
				t.Fatal("startup failure did not close source exactly once")
			}
		})
	}
}
