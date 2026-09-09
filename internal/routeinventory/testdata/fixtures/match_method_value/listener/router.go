package listener

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

type sealedHandler struct{ h http.Handler }

func (s sealedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.h.ServeHTTP(w, r) }
func NewRouter() http.Handler                                            { return sealedHandler{h: newRouter()} }
func newRouter() chi.Router {
	r := chi.NewRouter()
	capture(r.Match)
	r.Get("/ok", func(w http.ResponseWriter, r *http.Request) {})
	return r
}
func capture(value any) {}
