package handlers

import (
	"bytes"
	"io"
	"net/http"
)

// dispatchBloemLifecycle routes a Silo handler to its Bloem durable
// lifecycle-receipt path when that path is wired (run), and otherwise refuses
// an Idempotency-Key the server cannot honor. It reports whether it wrote the
// response, so the Silo handler needs only a single hook line.
func dispatchBloemLifecycle(w http.ResponseWriter, r *http.Request, wired bool, run func()) bool {
	if wired {
		run()
		return true
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return true
	}
	return false
}

// bufferBloemRequestBodyLimit reads at most limit body bytes for lifecycle
// digesting and puts them back on the request for the Silo decoder. A read
// failure answers the handler's usual 400.
func bufferBloemRequestBodyLimit(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, true
}
