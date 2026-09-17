package jellycompat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/livetv"
)

type countingLiveBridge struct {
	starts atomic.Int32
}

func (b *countingLiveBridge) StartLiveStream(context.Context, livetv.LiveStreamRequest) (string, string, error) {
	b.starts.Add(1)
	return "pb-unused", "/api/v1/livetv/live-hls/pb-unused/index.m3u8", nil
}

func rawStreamFixture(t *testing.T) (*LiveTVHandler, *livetvTestStore, *countingLiveBridge) {
	t.Helper()
	store := newLivetvTestStore()
	store.tuners = []livetv.Tuner{{ID: "t1", Type: livetv.TunerTypeHDHomeRun, TunerCount: 1, Status: "ready"}}
	store.channels["ch1"] = livetv.Channel{ID: "ch1", TunerID: "t1", Enabled: true, StreamURL: "http://192.168.1.2/auto/v1"}
	h := newTestLiveTVHandler(store)
	bridge := &countingLiveBridge{}
	h.service.SetPlaybackBridge(bridge)
	return h, store, bridge
}

// Compat clients read the tuner's MPEG-TS through HandleLiveStreamFile. Opening
// the stream used to start the native HLS remux as well, which nobody read.
func TestLiveTVCompatTuneDoesNotStartHLSBridge(t *testing.T) {
	h, _, bridge := rawStreamFixture(t)

	source, err := h.openChannelStream(context.Background(), &Session{Token: "tok", ProfileID: "p1"}, "ch1")
	if err != nil {
		t.Fatalf("openChannelStream: %v", err)
	}
	if got := bridge.starts.Load(); got != 0 {
		t.Fatalf("HLS bridge starts = %d, want 0 for a compat tune", got)
	}
	h.mu.Lock()
	stream := h.streams[source.LiveStreamID]
	h.mu.Unlock()
	if stream == nil || stream.SourceURL != "http://192.168.1.2/auto/v1" || stream.NativeSession == "" {
		t.Fatalf("compat stream = %+v, want the raw tuner URL and a native session", stream)
	}
}

// A compat stream whose native session is already gone must not start pulling
// from the tuner: the lease's first renewal fails closed before any bytes flow.
func TestLiveTVCompatStreamRefusesReleasedSession(t *testing.T) {
	h, store, _ := rawStreamFixture(t)
	source, err := h.openChannelStream(context.Background(), &Session{Token: "tok"}, "ch1")
	if err != nil {
		t.Fatalf("openChannelStream: %v", err)
	}
	h.mu.Lock()
	native := h.streams[source.LiveStreamID].NativeSession
	h.mu.Unlock()
	if _, err := store.ReleaseSession(context.Background(), native); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/LiveTv/LiveStreamFiles/"+source.LiveStreamID+"/stream.ts", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", source.LiveStreamID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, compatSessionKey, &Session{Token: "tok"})
	rec := httptest.NewRecorder()
	h.HandleLiveStreamFile(rec, req.WithContext(ctx))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("stream for released session = %d, want 404", rec.Code)
	}
}
