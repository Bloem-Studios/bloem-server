// Package listener is an analyzer fixture, not shipped code. A helper that
// mounts itself would never finish enumerating, so the walk refuses it.
package listener

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type sealedHandler struct{ h http.Handler }

func (s sealedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.h.ServeHTTP(w, r) }

func NewRouter() http.Handler { return sealedHandler{h: newRouter()} }

func newRouter() chi.Router {
	r := chi.NewRouter()
	mountLoop(r)
	return r
}

func mountLoop(r chi.Router) {
	r.Get("/items", func(w http.ResponseWriter, r *http.Request) {})
	r.Route("/again", func(r chi.Router) { mountLoop(r) })
}
