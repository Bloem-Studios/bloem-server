package orgdomains

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/clientip"
)

// DirectoryTTL is how long a directory serves what it read. A write on this
// node invalidates it at once; other nodes see the write within this time.
const DirectoryTTL = 30 * time.Second

// loadTimeout bounds one directory read, independent of the request that
// triggered it.
const loadTimeout = 5 * time.Second

// Directory is the in-memory map of registered domains and organization
// branding values. A nil *Directory maps nothing.
type Directory struct {
	load       func(context.Context) (snapshot, error)
	ttl        time.Duration
	now        func() time.Time
	mu         sync.Mutex
	cur        atomic.Pointer[loadedSnapshot]
	stale      atomic.Bool
	refreshing atomic.Bool
}

type loadedSnapshot struct {
	snap snapshot
	at   time.Time
}

func NewDirectory(store *Store, ttl time.Duration) *Directory {
	return newDirectory(store.loadSnapshot, ttl)
}

func newDirectory(load func(context.Context) (snapshot, error), ttl time.Duration) *Directory {
	return &Directory{load: load, ttl: ttl, now: time.Now}
}

func (d *Directory) fresh(cur *loadedSnapshot) bool {
	return cur != nil && !d.stale.Load() && d.now().Sub(cur.at) < d.ttl
}

// snapshot is the current map. A map older than the time to live is still
// served while one background read replaces it, so a slow database never
// holds requests up. The first read, and the read after a local write
// (Invalidate), happen in the request, so the write is visible at once. A
// failed read keeps serving the previous map.
func (d *Directory) snapshot(ctx context.Context) snapshot {
	cur := d.cur.Load()
	if cur != nil && !d.stale.Load() {
		if d.now().Sub(cur.at) >= d.ttl && d.refreshing.CompareAndSwap(false, true) {
			// The read outlives the request that noticed the expiry, so it
			// keeps the request's values but not its cancellation.
			bg := context.WithoutCancel(ctx)
			go func() {
				defer d.refreshing.Store(false)
				d.mu.Lock()
				defer d.mu.Unlock()
				if latest := d.cur.Load(); !d.fresh(latest) {
					d.reload(bg, latest)
				}
			}()
		}
		return cur.snap
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	cur = d.cur.Load()
	if d.fresh(cur) {
		return cur.snap
	}
	return d.reload(ctx, cur)
}

// reload reads the map and stores it; on failure it stores cur's map again.
// The caller holds d.mu.
func (d *Directory) reload(ctx context.Context, cur *loadedSnapshot) snapshot {
	d.stale.Store(false)
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loadTimeout)
	defer cancel()
	snap, err := d.load(loadCtx)
	if err != nil {
		slog.WarnContext(ctx, "organization domains: read failed, serving the previous map", "error", err)
		if cur != nil {
			snap = cur.snap
		}
	}
	d.cur.Store(&loadedSnapshot{snap: snap, at: d.now()})
	return snap
}

// Lookup is the organization host is registered to. host may carry a port and
// any case.
func (d *Directory) Lookup(ctx context.Context, host string) (uuid.UUID, bool) {
	if d == nil {
		return uuid.Nil, false
	}
	key := requestHostKey(host)
	if key == "" {
		return uuid.Nil, false
	}
	org, ok := d.snapshot(ctx).hosts[key]
	return org, ok
}

// PrimaryHost is org's primary domain.
func (d *Directory) PrimaryHost(ctx context.Context, org uuid.UUID) (string, bool) {
	if d == nil {
		return "", false
	}
	host, ok := d.snapshot(ctx).primary[org]
	return host, ok
}

// BrandingValue is org's own value for a branding.* setting.
func (d *Directory) BrandingValue(ctx context.Context, org uuid.UUID, key string) (string, bool) {
	if d == nil {
		return "", false
	}
	value, ok := d.snapshot(ctx).branding[org][key]
	return value, ok
}

// IsDefault reports whether org is the deployment's default organization,
// which uses the server's own branding and settings.
func (d *Directory) IsDefault(ctx context.Context, org uuid.UUID) bool {
	if d == nil || org == uuid.Nil {
		return false
	}
	return d.snapshot(ctx).defaultOrg == org
}

// Invalidate makes the next read load the map again.
func (d *Directory) Invalidate() {
	if d != nil {
		d.stale.Store(true)
	}
}

// WrapListener maps every request on the listener to the organization of the
// domain it arrived on: the API router and the page shell, which is served
// outside the API router. The host is the one a trusted proxy forwarded, read
// without changing the request, so the API router's own client address
// resolution still sees the proxy. Without a directory it returns root.
func WrapListener(root http.Handler, resolver *clientip.Resolver, d *Directory) http.Handler {
	if d == nil {
		return root
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if org, ok := d.Lookup(r.Context(), resolver.RequestHostOf(r)); ok {
			r = r.WithContext(WithRequestOrganization(r.Context(), org))
		}
		root.ServeHTTP(w, r)
	})
}
