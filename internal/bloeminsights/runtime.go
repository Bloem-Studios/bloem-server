package bloeminsights

import "github.com/Silo-Server/silo-server/internal/bloemgeo"

// Runtime is a server's stream insights, built once at startup and shared
// by the observer, the request hook and the routes.
type Runtime struct {
	Store    *Store
	Facts    FactsStore
	Geo      *bloemgeo.Resolver
	Observer *Observer
	Refusals *RefusalRecorder
}
