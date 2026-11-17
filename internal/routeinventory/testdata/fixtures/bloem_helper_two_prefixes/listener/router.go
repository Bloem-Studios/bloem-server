// Package listener is an analyzer fixture, not shipped code. One helper is
// mounted at two prefixes; each call registers its own routes.
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
	r.Route("/a", func(r chi.Router) { mountItems(r) })
	r.Route("/b", func(r chi.Router) { mountItems(r) })
	return r
}

func mountItems(r chi.Router) {
	r.Get("/items", func(w http.ResponseWriter, r *http.Request) {})
}
