// Package listener is an analyzer fixture, not shipped code. Handlers drain
// the request body once (for a digest, say) and decode the same bytes again
// through bytes.NewReader or bytes.NewBuffer.
package listener

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type sealedHandler struct{ h http.Handler }

func (s sealedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.h.ServeHTTP(w, r) }

func NewRouter() http.Handler { return sealedHandler{h: newRouter()} }

func newRouter() chi.Router {
	r := chi.NewRouter()
	r.Post("/reader", reread)
	r.Post("/buffer", rebuffer)
	return r
}

func reread(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.NewDecoder(bytes.NewReader(raw)).Decode(&body)
	_ = json.NewEncoder(w).Encode(body)
}

func rebuffer(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.NewDecoder(bytes.NewBuffer(raw)).Decode(&body)
	_ = json.NewEncoder(w).Encode(body)
}
