package livetv

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type countingPlaybackBridge struct {
	starts atomic.Int32
}

func (b *countingPlaybackBridge) StartLiveStream(context.Context, LiveStreamRequest) (string, string, error) {
	b.starts.Add(1)
	return "pb-count", "/api/v1/livetv/live-hls/pb-count/index.m3u8", nil
}

// The Jellyfin surface proxies the tuner's MPEG-TS itself. It used to start the
// HLS remux anyway: a second tuner consumer, an ffmpeg process, and possibly an
// encode slot that nobody ever read from.
func TestStartRawChannelSessionSkipsPlaybackBridge(t *testing.T) {
	svc, _ := singleTunerService(t)
	bridge := &countingPlaybackBridge{}
	svc.SetPlaybackBridge(bridge)
	ctx := context.Background()

	session, err := svc.StartRawChannelSession(ctx, "ch1", 7, "p1")
	if err != nil {
		t.Fatalf("StartRawChannelSession: %v", err)
	}
	if got := bridge.starts.Load(); got != 0 {
		t.Fatalf("bridge starts = %d, want 0", got)
	}
	if session.PlaybackSessionID != "" || session.Transport != "mpegts" {
		t.Fatalf("raw session = playback %q transport %q, want no playback id and mpegts",
			session.PlaybackSessionID, session.Transport)
	}
	if session.TunerID != "t1" || session.Status != "active" {
		t.Fatalf("raw session did not claim the tuner: %+v", session)
	}
	// The claim counts against capacity like any other tune.
	if _, err := svc.StartRawChannelSession(ctx, "ch1", 8, "p2"); !errors.Is(err, ErrNoTuner) {
		t.Fatalf("second raw tune on a full tuner = %v, want ErrNoTuner", err)
	}

	if _, err := svc.ReleaseSession(ctx, session.ID, 0, "", false); err != nil {
		t.Fatalf("ReleaseSession: %v", err)
	}
	if _, err := svc.StartChannelSession(ctx, "ch1", 7, "p1", ClientCapabilities{}); err != nil {
		t.Fatalf("native StartChannelSession: %v", err)
	}
	if got := bridge.starts.Load(); got != 1 {
		t.Fatalf("native tune bridge starts = %d, want 1", got)
	}
}
