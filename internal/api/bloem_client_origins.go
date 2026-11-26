package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// bloemClientOrigins are the web origins of client apps that read the native
// API from a page held to CORS.
//
// Moonfin's Xbox app (Moonfin-Client/Smart-TV 3.1.x) runs its page at
// moonfin.internal inside a WebView that enforces CORS, and sends its Silo
// requests from that page. Silo answers no cross-origin requests, so the
// browser's preflight got a 405 and the app reported it could not connect.
// Moonfin's next release sends those requests through its native shell; until
// every console has it, this lets that one origin read /api/v2.
//
// No other website can serve a page from these origins, and the app
// authenticates with its own bearer token, so credentials are never allowed:
// no cookie of a signed-in browser goes along.
var bloemClientOrigins = map[string]bool{
	"http://moonfin.internal":  true,
	"https://moonfin.internal": true,
}

const (
	bloemClientOriginPath       = "/api/v2/"
	bloemClientOriginMethods    = "GET, HEAD, POST, PUT, PATCH, DELETE"
	bloemClientOriginMaxAge     = "600"
	bloemClientOriginMaxHeaders = 2048
)

// useBloemClientOrigins answers the CORS preflight of an allowed client
// origin for the native API and marks that origin's responses readable.
// Every other request passes through unchanged.
func useBloemClientOrigins(r chi.Router) {
	r.Use(bloemClientOriginCORS)
}

func bloemClientOriginCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !bloemClientOrigins[origin] || !strings.HasPrefix(r.URL.Path, bloemClientOriginPath) {
			next.ServeHTTP(w, r)
			return
		}
		header := w.Header()
		header.Add("Vary", "Origin")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			header.Add("Vary", "Access-Control-Request-Method")
			header.Add("Vary", "Access-Control-Request-Headers")
			header.Set("Access-Control-Allow-Origin", origin)
			header.Set("Access-Control-Allow-Methods", bloemClientOriginMethods)
			if requested := bloemClientOriginRequestedHeaders(r.Header.Get("Access-Control-Request-Headers")); requested != "" {
				header.Set("Access-Control-Allow-Headers", requested)
			}
			header.Set("Access-Control-Max-Age", bloemClientOriginMaxAge)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		header.Set("Access-Control-Allow-Origin", origin)
		header.Set("Access-Control-Expose-Headers", "*")
		next.ServeHTTP(w, r)
	})
}

// bloemClientOriginRequestedHeaders echoes the header names a preflight asks
// for, keeping only valid field names, so the allowance follows whatever the
// client sends (Authorization, Content-Type, Silo's client and profile
// headers) without a list that drifts from Silo's.
func bloemClientOriginRequestedHeaders(requested string) string {
	if len(requested) > bloemClientOriginMaxHeaders {
		return ""
	}
	names := make([]string, 0, 8)
	for _, name := range strings.Split(requested, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !bloemClientOriginToken(name) {
			return ""
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func bloemClientOriginToken(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}
