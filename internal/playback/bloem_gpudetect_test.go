package playback

// Bloem hardware-probe coverage moved out of Silo's gpudetect_test.go.

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestExpiredFFmpegContextDoesNotLaunchProbe(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		configure func(*testing.T, *hwAccelTestEnv)
	}{
		{
			name: "Linux NVENC",
			goos: linuxGOOS,
			configure: func(t *testing.T, env *hwAccelTestEnv) {
				env.addRenderDevice(t, "renderD128", "0x10de")
			},
		},
		{name: "Darwin VideoToolbox", goos: darwinGOOS},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := setupHWAccelTest(t)
			currentGOOS = test.goos
			if test.configure != nil {
				test.configure(t, env)
			}

			// A removed top-level guard makes the VideoToolbox cache register a
			// detached flight synchronously. Park any such flight so the in-flight
			// count is deterministic rather than racing a fast helper failure.
			release := make(chan struct{})
			previousHWStarted := hwProbeFlightStarted
			previousVideoToolboxStarted := videoToolboxProbeStarted
			hwProbeFlightStarted = func() { <-release }
			videoToolboxProbeStarted = func() { <-release }
			defer func() {
				close(release)
				hwProbeFlightStarted = previousHWStarted
				videoToolboxProbeStarted = previousVideoToolboxStarted
			}()

			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			if got := ResolveHWAccelWithFFmpegContext(ctx, "auto", "/does/not/exist/ffmpeg", ""); got != HWAccelNone {
				t.Fatalf("ResolveHWAccelWithFFmpegContext() = %q, want none", got)
			}
			if ctx.Err() != context.DeadlineExceeded {
				t.Fatalf("context error = %v, want deadline exceeded", ctx.Err())
			}
			if inFlight := HWProbesInFlight(); inFlight != 0 {
				t.Fatalf("expired caller registered %d hardware probe(s), want none", inFlight)
			}
		})
	}
}

func waitForFakeFFmpegStart(t *testing.T, startedPath string, probeDone <-chan string) {
	t.Helper()
	// Keep the watchdog outside the probe command's own startup window. On a
	// loaded runner the shell may not write the marker immediately, but the
	// bounded probe will still report through probeDone if it cannot start.
	deadline := time.NewTimer(hwProbeCommandTimeout + time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		if _, err := os.Stat(startedPath); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat fake FFmpeg start signal: %v", err)
		}
		select {
		case result := <-probeDone:
			t.Fatalf("FFmpeg probe returned %q before its helper signaled process start", result)
		case <-deadline.C:
			t.Fatal("timed out waiting for fake FFmpeg process start")
		case <-ticker.C:
		}
	}
}
