// Package serverid owns this deployment's stable identity: the value clients
// key their stored per-server state on, and the value the local-network
// advertisement publishes. It lives in its own package precisely so those two
// cannot diverge — both take the identifier from one resolver rather than
// re-deriving it, which a method hanging off an HTTP handler could not offer to
// a process-level subsystem.
package serverid

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// SettingKey is the server_settings key holding the identifier. It is a PLAIN
// row on purpose: it is not a secret (every client that reaches the server is
// told the value), and encrypted rows are GCM-bound to their key name, which
// would make the identifier unreadable after any future key rename.
//
// No migration seeds it. It is minted on first read, so a fresh install and an
// upgraded install follow the identical path, and a restored backup keeps the
// identifier it already had — correctly, because it is the same server.
const SettingKey = "server.instance_id"

// ErrUnavailable is returned when there is no settings store to resolve
// against, which is the no-database case. Callers must surface it rather than
// substitute a value: an identifier that changes between processes — or worse,
// between requests — silently re-keys every client's stored state.
var ErrUnavailable = errors.New("serverid: no settings store")

// Store is the settings surface the resolver needs. catalog.SettingsStore and
// the encrypting decorator around it both satisfy it.
type Store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// conditionalStore is the optional insert-if-absent surface used to mint the
// identifier without racing a concurrent node. Both catalog.ServerSettingsRepo
// and catalog.EncryptedSettingsRepo satisfy it.
type conditionalStore interface {
	SetIfAbsent(ctx context.Context, key, value string) (bool, error)
}

// Resolver resolves the identifier once per process and remembers it.
type Resolver struct {
	store Store

	// mu guards id, and is held across the first resolution so concurrent
	// first callers mint at most one candidate between them. Every later call
	// is a memory read: the public identity endpoint would otherwise do a
	// database round trip per unauthenticated request.
	mu sync.Mutex
	id string
}

// NewResolver constructs a resolver over a settings store. A nil store is
// allowed and makes every Resolve return ErrUnavailable, so wiring without a
// database needs no special case at the call site.
func NewResolver(store Store) *Resolver {
	return &Resolver{store: store}
}

// Resolve returns the stable identifier, minting and persisting one on first
// use. It never returns a value it has not confirmed is the stored one, and it
// never returns a different value on a later call.
func (r *Resolver) Resolve(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.id != "" {
		return r.id, nil
	}
	if r.store == nil {
		return "", ErrUnavailable
	}

	existing, err := r.store.Get(ctx, SettingKey)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", SettingKey, err)
	}
	if id := strings.TrimSpace(existing); id != "" {
		r.id = id
		return id, nil
	}

	minted := uuid.NewString()
	writer, ok := r.store.(conditionalStore)
	if !ok {
		if err := r.store.Set(ctx, SettingKey, minted); err != nil {
			return "", fmt.Errorf("write %s: %w", SettingKey, err)
		}
		r.id = minted
		return minted, nil
	}

	won, err := writer.SetIfAbsent(ctx, SettingKey, minted)
	if err != nil {
		return "", fmt.Errorf("write %s: %w", SettingKey, err)
	}
	if won {
		// Winning the insert is itself proof that the minted value is the
		// stored one; a read-back would only cost a round trip.
		r.id = minted
		return minted, nil
	}

	// Another node won the insert. Adopt its value rather than serve one that
	// is not in the database.
	winner, err := r.store.Get(ctx, SettingKey)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", SettingKey, err)
	}
	if id := strings.TrimSpace(winner); id != "" {
		r.id = id
		return id, nil
	}
	// The write did not land and neither did anyone else's. Refuse: serving the
	// minted value here would hand out a different identifier on every request,
	// which is the one failure mode worse than having none.
	return "", fmt.Errorf("%s is still empty after a successful write", SettingKey)
}
