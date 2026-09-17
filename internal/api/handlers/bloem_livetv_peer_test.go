package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveTVPeerRelaysCredentialsAndRangeOneHop(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/livetv/live-hls/p/seg.ts" || r.URL.Query().Get("st") != "signed" || r.Header.Get("Authorization") != "Bearer owner" || r.Header.Get("X-Profile-Id") != "profile" || r.Header.Get(liveTVPeerHop) != "1" || r.Header.Get("Range") != "bytes=0-3" {
			t.Errorf("peer request lost routing or authority")
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Content-Range", "bytes 0-3/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "data")
	}))
	defer peer.Close()
	r := httptest.NewRequest("GET", "/api/v1/livetv/live-hls/p/seg.ts?st=signed", nil)
	r.Header.Set("Authorization", "Bearer owner")
	r.Header.Set("X-Profile-Id", "profile")
	r.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	serveLiveTVPeer(w, r, peer.URL)
	if w.Code != 206 || w.Body.String() != "data" || w.Header().Get("Content-Range") == "" {
		t.Fatalf("relay = %d %s", w.Code, w.Body.String())
	}
}

func TestLiveTVPeerRefusesLoopsAndRedirects(t *testing.T) {
	calls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "https://untrusted.invalid", http.StatusTemporaryRedirect)
	}))
	defer peer.Close()
	for _, hop := range []string{"1", ""} {
		r := httptest.NewRequest("GET", "/api/v1/livetv/live-hls/p/index.m3u8?st=signed", nil)
		r.Header.Set(liveTVPeerHop, hop)
		w := httptest.NewRecorder()
		serveLiveTVPeer(w, r, peer.URL)
		if w.Code != 503 || w.Header().Get("Location") != "" || w.Header().Get("Retry-After") == "" {
			t.Fatalf("unsafe response: %d %v", w.Code, w.Header())
		}
	}
	if calls != 1 {
		t.Fatalf("loop followed: calls=%d", calls)
	}
	peer.Close()
	w := httptest.NewRecorder()
	serveLiveTVPeer(w, httptest.NewRequest("GET", "/x", nil), peer.URL)
	if w.Code != 503 {
		t.Fatalf("dead peer: %d", w.Code)
	}
}
