package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloeminsights"
	"github.com/Silo-Server/silo-server/internal/clientip"
)

type observedFacts struct {
	session string
	facts   bloeminsights.RequestFacts
}

func installTestStreamInsights(t *testing.T, observe func(string, bloeminsights.RequestFacts)) {
	t.Helper()
	cidrs, err := clientip.ParseCIDRs(clientip.DefaultTrustedProxies)
	if err != nil {
		t.Fatal(err)
	}
	SetStreamInsightsObserver(observe, clientip.NewResolver(cidrs), streamInsightsTestSecret)
	t.Cleanup(func() { SetStreamInsightsObserver(nil, nil, "") })
}

func serveInsightsRequest(t *testing.T, target, remote string, status int, header http.Header) {
	t.Helper()
	handler := captureNativeVideoRequest(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status == http.StatusOK {
			_, _ = w.Write([]byte("media"))
			return
		}
		w.WriteHeader(status)
	}))
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = remote
	for key, values := range header {
		req.Header[key] = values
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
}

func nextObserved(t *testing.T, got <-chan observedFacts) observedFacts {
	t.Helper()
	select {
	case o := <-got:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("no facts observed")
		return observedFacts{}
	}
}

// TestStreamInsightsHookRecordsAuthorizedMediaOnly checks the hook records
// a media request's facts once the request was answered without an error,
// takes the edge header only from a trusted peer, and records nothing for a
// refused request or one that is not media.
func TestStreamInsightsHookRecordsAuthorizedMediaOnly(t *testing.T) {
	got := make(chan observedFacts, 16)
	installTestStreamInsights(t, func(session string, f bloeminsights.RequestFacts) { got <- observedFacts{session, f} })
	edge := http.Header{bloeminsights.EdgeHeader: {"name=ams-1; lat=52.37; lon=4.89; viewer_proto=h3"}}

	serveInsightsRequest(t, "/api/v2/playback/transcode/sess-1/segment/seg_00001.m4s", "127.0.0.2:51000", http.StatusOK, edge)
	first := nextObserved(t, got)
	if first.session != "sess-1" || first.facts.Edge == nil || first.facts.Edge.ViewerProto != "h3" {
		t.Fatalf("first = %+v, want sess-1 through the trusted edge", first)
	}

	serveInsightsRequest(t, "/api/v2/stream/sess-2", "127.0.0.2:51000", http.StatusUnauthorized, edge)
	serveInsightsRequest(t, "/api/v2/items/abc", "127.0.0.2:51000", http.StatusOK, edge)
	serveInsightsRequest(t, "/api/v2/stream/sess-3", "203.0.113.9:51000", http.StatusPartialContent, edge)
	third := nextObserved(t, got)
	if third.session != "sess-3" {
		t.Fatalf("after a refused and a non-media request, observed %q, want sess-3", third.session)
	}
	if third.facts.Edge != nil {
		t.Fatalf("an untrusted peer's edge header was kept: %+v", third.facts.Edge)
	}
	select {
	case extra := <-got:
		t.Fatalf("unexpected observation %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestStreamInsightsHookIsOffUntilInstalled(t *testing.T) {
	SetStreamInsightsObserver(nil, nil, "")
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/stream/sess-1", nil)
	if w := observeStreamInsights(recorder, req, ""); w != http.ResponseWriter(recorder) {
		t.Fatal("the hook wrapped a response with no observer installed")
	}
}

// The wrapped response keeps flushing and unwrapping, which media serving
// and http.ResponseController rely on.
func TestStreamInsightsHookKeepsTheResponseWriterUsable(t *testing.T) {
	installTestStreamInsights(t, func(string, bloeminsights.RequestFacts) {})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/stream/sess-1", nil)
	w := observeStreamInsights(recorder, req, "")
	if w == http.ResponseWriter(recorder) {
		t.Fatal("a media request was not observed")
	}
	if err := http.NewResponseController(w).Flush(); err != nil {
		t.Fatalf("flush through the hook: %v", err)
	}
	if !recorder.Flushed {
		t.Fatal("the underlying writer was not flushed")
	}
}

// A full queue drops facts instead of holding up the media request.
func TestStreamInsightsHookDropsWhenTheQueueIsFull(t *testing.T) {
	release := make(chan struct{})
	installTestStreamInsights(t, func(string, bloeminsights.RequestFacts) { <-release })
	defer close(release)
	done := make(chan struct{})
	go func() {
		for range streamInsightsQueueSize + 50 {
			serveInsightsRequest(t, "/api/v2/stream/sess-1", "203.0.113.9:51000", http.StatusOK, nil)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("media requests blocked on a full queue")
	}
	if streamInsightsDropped() == 0 {
		t.Fatal("a full queue dropped nothing")
	}
}
