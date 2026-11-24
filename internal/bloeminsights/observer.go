package bloeminsights

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemgeo"
	evt "github.com/Silo-Server/silo-server/internal/events"
)

// Observer keeps a stream record for every playback session from the
// events hub's lifecycle events, as AdminPushService.WatchEvents follows the
// same hub.
type Observer struct {
	store   *Store
	geo     *bloemgeo.Resolver
	facts   FactsStore
	logger  *slog.Logger
	applied atomic.Int64
	// ready, when set, is closed once Run has subscribed (for tests).
	ready chan struct{}
}

// NewObserver builds an observer. geo and facts may be nil.
func NewObserver(store *Store, geo *bloemgeo.Resolver, facts FactsStore, logger *slog.Logger) *Observer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Observer{store: store, geo: geo, facts: facts, logger: logger}
}

// Applied counts the lifecycle events the observer wrote.
func (o *Observer) Applied() int64 { return o.applied.Load() }

func lifecycleEvent(name string) bool {
	switch name {
	case "session.started", "session.paused", "session.resumed", "session.stopped":
		return true
	}
	return false
}

// Run follows hub until ctx ends.
func (o *Observer) Run(ctx context.Context, hub *evt.Hub) {
	if o == nil || o.store == nil || hub == nil {
		return
	}
	events, cancel := hub.Subscribe()
	defer cancel()
	if o.ready != nil {
		close(o.ready)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-events:
			if !ok {
				return
			}
			if env.Channel != evt.ChannelSessions || !lifecycleEvent(env.Event) {
				continue
			}
			ev, err := DecodeLifecycleEvent(env.Data)
			if err != nil {
				o.logger.WarnContext(ctx, "stream insights could not read a lifecycle event", "event", env.Event, "error", err)
				continue
			}
			o.apply(ctx, ev)
		}
	}
}

func (o *Observer) apply(ctx context.Context, ev LifecycleEvent) {
	geo := o.geo.Lookup(ev.ClientIP())
	var summary *FactsSummary
	if o.facts != nil {
		s, ok, err := o.facts.Summary(ctx, ev.SessionID)
		if err != nil {
			o.logger.WarnContext(ctx, "stream insights could not read request facts", "session", ev.SessionID, "error", err)
		} else if ok {
			summary = &s
		}
	}
	if err := o.store.ApplyLifecycle(ctx, ev, geo, summary); err != nil {
		if ctx.Err() == nil {
			o.logger.WarnContext(ctx, "stream insights could not record a lifecycle event", "session", ev.SessionID, "error", err)
		}
		return
	}
	o.applied.Add(1)
}

// PruneLoop deletes rows older than keep now and every period until ctx
// ends.
func (o *Observer) PruneLoop(ctx context.Context, every, keep time.Duration) {
	if o == nil || o.store == nil {
		return
	}
	prune := func() {
		if deleted, err := o.store.Prune(ctx, time.Now().Add(-keep)); err != nil {
			if ctx.Err() == nil {
				o.logger.WarnContext(ctx, "stream insights prune failed", "error", err)
			}
		} else if deleted > 0 {
			o.logger.InfoContext(ctx, "stream insights pruned old rows", "rows", deleted)
		}
	}
	prune()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}
