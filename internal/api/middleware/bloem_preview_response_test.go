package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestPreviewResponseOmitsChapterCovers(t *testing.T) {
	r := httptest.NewRequest("GET", "/catalog", nil).WithContext(withPreviewPrincipal(httptest.NewRequest("GET", "/catalog", nil).Context()))
	w := httptest.NewRecorder()
	servePreviewResponse(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"poster_url":"cover","chapters":[{"poster_url":"chapter-secret","poster_thumbhash":"hash","title":"Chapter one"}]}`))
	}), w, r)
	if strings.Contains(w.Body.String(), "chapter-secret") || strings.Contains(w.Body.String(), "hash") || !strings.Contains(w.Body.String(), `"poster_url":"cover"`) {
		t.Fatal(w.Body.String())
	}
}

func TestPreviewResponseBoundsSerializedOutput(t *testing.T) {
	r := httptest.NewRequest("GET", "/catalog", nil)
	r = r.WithContext(withPreviewPrincipal(r.Context()))
	w := httptest.NewRecorder()
	servePreviewResponse(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"overview":"` + strings.Repeat("<", 2<<20) + `"}`))
	}), w, r)
	if w.Code != 503 || w.Body.Len() > maxPreviewResponseBytes {
		t.Fatalf("serialized output exceeded bound: status=%d bytes=%d", w.Code, w.Body.Len())
	}
}

type previewDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *previewDeadlineWriter) SetReadDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func TestPreviewResponsePreservesReadDeadlineControl(t *testing.T) {
	r := httptest.NewRequest("POST", "/catalog/query", nil)
	r = r.WithContext(withPreviewPrincipal(r.Context()))
	w := &previewDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	deadline := time.Now().Add(time.Minute)
	servePreviewResponse(http.HandlerFunc(func(captured http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(captured).SetReadDeadline(deadline); err != nil {
			t.Errorf("preview lost deadline control: %v", err)
		}
		_, _ = captured.Write([]byte(`{}`))
	}), w, r)
	if !w.deadline.Equal(deadline) {
		t.Fatal("read deadline was not applied")
	}
}
