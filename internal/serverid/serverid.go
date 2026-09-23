// Package serverid is Bloem's name for this deployment's stable identity: the
// value clients key their stored per-server state on, and the value the
// local-network advertisement publishes.
//
// It is a thin adapter over upstream Silo's internal/serveridentity, which owns
// the setting row (serveridentity.Key), the minting algorithm and the
// per-process cache. Bloem originally kept its own resolver under
// "server.instance_id"; migration 20260923120000_bloem_converge_server_identity
// copied that value onto upstream's row, and every resolution now goes through
// serveridentity. The package survives only because the Bloem identity handler
// (internal/api/handlers/server_identity.go) and the invitation/auth lifecycle
// seams take a Resolve-shaped identity; new code should use
// *serveridentity.Service directly.
package serverid

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/serveridentity"
)

// SettingKey is the server_settings key holding the identifier, shared with
// upstream Silo.
const SettingKey = serveridentity.Key

// ErrUnavailable is returned when there is no settings store to resolve
// against, which is the no-database case.
var ErrUnavailable = serveridentity.ErrUnavailable

// Store is the settings surface the identity needs.
type Store = serveridentity.Store

// Resolver adapts a *serveridentity.Service to the Resolve-shaped identity
// Bloem's handlers take.
type Resolver struct {
	service *serveridentity.Service
}

// NewResolver constructs a resolver over a settings store. A nil store is
// allowed and makes every Resolve return ErrUnavailable.
func NewResolver(store Store) *Resolver {
	return FromService(serveridentity.New(store))
}

// FromService adapts an existing identity service, so a caller that already
// holds one shares its cache.
func FromService(service *serveridentity.Service) *Resolver {
	return &Resolver{service: service}
}

// Resolve returns the stable identifier, minting and persisting one on first
// use.
func (r *Resolver) Resolve(ctx context.Context) (string, error) {
	if r == nil {
		return "", ErrUnavailable
	}
	return r.service.ServerID(ctx)
}

// ServerID is Resolve under serveridentity's name, so a Resolver satisfies the
// same interfaces a *serveridentity.Service does.
func (r *Resolver) ServerID(ctx context.Context) (string, error) {
	return r.Resolve(ctx)
}
