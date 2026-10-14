package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const maxPreviewResponseBytes = 8 << 20

type previewResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *previewResponse) Header() http.Header { return w.header }
func (w *previewResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *previewResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.overflow || len(p) > maxPreviewResponseBytes-w.body.Len() {
		w.overflow = true
		w.body.Reset()
		return len(p), nil
	}
	return w.body.Write(p)
}

// Only preview JSON responses are buffered. Subscriber bytes and streaming
// interfaces pass directly to the original writer.
// PreviewResponses protects a response after authentication has installed the marker.
func PreviewResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { servePreviewResponse(next, w, r) })
}

func servePreviewResponse(next http.Handler, w http.ResponseWriter, r *http.Request) {
	if !IsPreviewPrincipal(r.Context()) {
		next.ServeHTTP(w, r)
		return
	}
	captured := &previewResponse{header: w.Header().Clone()}
	next.ServeHTTP(captured, r)
	status := captured.status
	if status == 0 {
		status = http.StatusOK
	}
	body := captured.body.Bytes()
	if captured.overflow {
		writePreviewResponseUnavailable(w)
		return
	}
	if len(body) > 0 {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			writePreviewResponseUnavailable(w)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writePreviewResponseUnavailable(w)
			return
		}
		if err := scrubPreviewJSON(value, 0); err != nil {
			writePreviewResponseUnavailable(w)
			return
		}
		var err error
		body, err = json.Marshal(value)
		if err != nil {
			writePreviewResponseUnavailable(w)
			return
		}
	}
	for name := range w.Header() {
		w.Header().Del(name)
	}
	for name, values := range captured.header {
		w.Header()[name] = append([]string(nil), values...)
	}
	w.Header().Del("Content-Length")
	w.Header().Del("ETag")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

func scrubPreviewJSON(value any, depth int) error {
	if depth > 128 {
		return errors.New("preview response nesting exceeds limit")
	}
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			lower := strings.ToLower(key)
			if lower == "file_name" || lower == "filename" || lower == "release_name" || lower == "path" || lower == "paths" || strings.HasSuffix(lower, "_path") || strings.HasSuffix(lower, "_paths") || lower == "thumbnail_url" || lower == "thumbnail_thumbhash" {
				delete(v, key)
				continue
			}
			if err := scrubPreviewJSON(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := scrubPreviewJSON(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func writePreviewResponseUnavailable(w http.ResponseWriter) {
	w.Header().Del("Content-Length")
	w.Header().Del("ETag")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"preview_response_unavailable","message":"This preview response is unavailable."}`))
}
