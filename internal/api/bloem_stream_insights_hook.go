package api

import (
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/bloeminsights"
	"github.com/Silo-Server/silo-server/internal/clientip"
)

// streamInsightsQueueSize bounds the media requests waiting to be recorded.
// A full queue drops the newest, so a media request never waits on it.
const streamInsightsQueueSize = 4096

type streamInsightsItem struct {
	path  string
	token string
	facts bloeminsights.RequestFacts
}

type streamInsightsHook struct {
	observe  func(sessionID string, f bloeminsights.RequestFacts)
	resolver *clientip.Resolver
	secret   string
	queue    chan streamInsightsItem
	stop     chan struct{}
}

var (
	currentStreamInsights atomic.Pointer[streamInsightsHook]
	streamInsightsDrops   atomic.Int64
)

// SetStreamInsightsObserver installs the function stream insights records
// media request facts with; nil removes it. resolver decides which transport
// peers are trusted proxies, and secret verifies stream tokens. fn runs on
// one background goroutine, never on the request path.
func SetStreamInsightsObserver(fn func(sessionID string, f bloeminsights.RequestFacts), resolver *clientip.Resolver, secret string) {
	var next *streamInsightsHook
	if fn != nil {
		next = &streamInsightsHook{observe: fn, resolver: resolver, secret: secret,
			queue: make(chan streamInsightsItem, streamInsightsQueueSize), stop: make(chan struct{})}
		go next.run()
	}
	if previous := currentStreamInsights.Swap(next); previous != nil {
		close(previous.stop)
	}
}

// streamInsightsDropped counts the facts a full queue dropped.
func streamInsightsDropped() int64 { return streamInsightsDrops.Load() }

func (h *streamInsightsHook) run() {
	for {
		select {
		case <-h.stop:
			return
		case item := <-h.queue:
			if sessionID, ok := mediaRequestSessionID(item.path, item.token, h.secret); ok {
				h.observe(sessionID, item.facts)
			}
		}
	}
}

// observeStreamInsights returns the writer a media request is served
// through. Once the response status shows the request was authorized
// (below 400), the request's facts are queued for its playback session. A
// request that is not media, or a server without stream insights, keeps w.
func observeStreamInsights(w http.ResponseWriter, r *http.Request, streamToken string) http.ResponseWriter {
	hook := currentStreamInsights.Load()
	if hook == nil {
		return w
	}
	if _, delivery := middleware.DeliverySessionID(r.URL.Path); !delivery && streamToken == "" {
		return w
	}
	facts := bloeminsights.FactsFromRequest(r, hook.resolver.PeerTrusted(r), cloudflarePeer(r), time.Now())
	return &insightsResponseWriter{ResponseWriter: w, hook: hook, item: streamInsightsItem{path: r.URL.Path, token: streamToken, facts: facts}}
}

func cloudflarePeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && bloeminsights.IsCloudflareAddress(addr)
}

// insightsResponseWriter queues its request's facts when the final status
// is written. It keeps Flush, ReadFrom (sendfile) and Unwrap, so media
// serving and http.ResponseController behave as without it.
type insightsResponseWriter struct {
	http.ResponseWriter
	hook    *streamInsightsHook
	item    streamInsightsItem
	decided bool
}

func (w *insightsResponseWriter) decide(status int) {
	if w.decided || status < http.StatusOK {
		return
	}
	w.decided = true
	if status >= http.StatusBadRequest {
		return
	}
	select {
	case w.hook.queue <- w.item:
	default:
		streamInsightsDrops.Add(1)
	}
}

func (w *insightsResponseWriter) WriteHeader(status int) {
	w.decide(status)
	w.ResponseWriter.WriteHeader(status)
}

func (w *insightsResponseWriter) Write(p []byte) (int, error) {
	w.decide(http.StatusOK)
	return w.ResponseWriter.Write(p)
}

func (w *insightsResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	w.decide(http.StatusOK)
	if from, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return from.ReadFrom(src)
	}
	return io.Copy(w.ResponseWriter, src)
}

func (w *insightsResponseWriter) Flush() {
	w.decide(http.StatusOK)
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *insightsResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
