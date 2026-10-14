package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewResponseOmitsNestedFileIdentity(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", "unsafe")
		_, _ = w.Write([]byte(`{"title":"Example","versions":[{"file_name":"private.mkv","release_name":"private-release","source":{"path":"/srv/media/private.mkv"},"resolution":"2160p","hdr":true,"audio_languages":["en"],"subtitle_languages":["fr"]}],"trailer":{"url":"https://www.youtube.com/watch?v=example"}}`))
	})
	r := httptest.NewRequest("GET", "/catalog", nil)
	r = r.WithContext(withPreviewPrincipal(r.Context()))
	w := httptest.NewRecorder()
	servePreviewResponse(next, w, r)
	for _, leak := range []string{"file_name", "release_name", "private.mkv", "/srv/media"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("leaked %s: %s", leak, w.Body)
		}
	}
	for _, kept := range []string{`"title":"Example"`, `"resolution":"2160p"`, `"hdr":true`, `"audio_languages":["en"]`, `"subtitle_languages":["fr"]`, `https://www.youtube.com/watch?v=example`} {
		if !strings.Contains(w.Body.String(), kept) {
			t.Errorf("lost metadata %s", kept)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
		t.Fatal("preview response is cacheable")
	}
}

func TestPreviewResponsePreservesSubscribers(t *testing.T) {
	const body = `{ "file_name": "subscriber.mkv", "path": "/srv/media/subscriber.mkv" }`
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "subscriber")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(body))
	})
	r := httptest.NewRequest("GET", "/catalog", nil)
	w := httptest.NewRecorder()
	servePreviewResponse(next, w, r)
	if w.Code != 202 || w.Body.String() != body || w.Header().Get("ETag") != "subscriber" {
		t.Fatalf("subscriber changed: %d %s", w.Code, w.Body)
	}
}

func TestPreviewResponseFailsClosedForMalformedOrOversizedJSON(t *testing.T) {
	for name, body := range map[string]string{"malformed": `{"file_name":"secret"`, "oversized": `{"description":"` + strings.Repeat("x", 9<<20) + `","file_name":"secret"}`} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/catalog", nil)
			r = r.WithContext(withPreviewPrincipal(r.Context()))
			w := httptest.NewRecorder()
			servePreviewResponse(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}), w, r)
			if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("unsafe response escaped: status%d", w.Code)
			}
		})
	}
}
