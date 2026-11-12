package libraryingest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// seriesProbes paces the episode probes of TV libraries. A whole remote TV
// catalog is millions of files and every probe reads megabytes, so a scan
// publishes gradually: each scan probes at most perScan new or changed
// episodes, at most perSecond per storage source across all scans, and the
// rest stay listed but unpublished until a later scan. Unchanged episodes are
// never probed and do not count. Shows appear as their episodes are probed,
// in listing order.
type seriesProbes struct {
	perScan   int
	perSecond rate.Limit
	burst     int
	limiters  sync.Map // source key -> *rate.Limiter
}

// ConfigureNativeSeriesProbes is startup-only, before scans are admitted.
// perScan 0 removes the per-scan budget; perSecond 0 removes pacing.
func (c *StorageScanner) ConfigureNativeSeriesProbes(perScan int, perSecond float64, burst int) error {
	if c == nil || perScan < 0 || perSecond < 0 || burst < 0 || perSecond > 0 && burst == 0 {
		return errors.New("invalid native series probe limits")
	}
	c.seriesProbes = &seriesProbes{perScan: perScan, perSecond: rate.Limit(perSecond), burst: burst}
	return nil
}

// seriesProbeGate is one scan's share of its source's probe limits.
type seriesProbeGate struct {
	remaining atomic.Int64
	budgeted  bool
	deferred  int64
	limiter   *rate.Limiter
}

func (c *StorageScanner) newSeriesProbeGate(source uuid.UUID) *seriesProbeGate {
	gate := &seriesProbeGate{}
	p := c.seriesProbes
	if p == nil {
		return gate
	}
	if p.perScan > 0 {
		gate.budgeted = true
		gate.remaining.Store(int64(p.perScan))
	}
	if p.perSecond > 0 {
		stored, _ := p.limiters.LoadOrStore(source, rate.NewLimiter(p.perSecond, p.burst))
		if limiter, ok := stored.(*rate.Limiter); ok {
			gate.limiter = limiter
		}
	}
	return gate
}

// admit takes one probe from the scan's budget. A refused probe is counted as
// deferred: its file waits for a later scan.
func (g *seriesProbeGate) admit() bool {
	if g == nil || !g.budgeted {
		return true
	}
	if g.remaining.Add(-1) >= 0 {
		return true
	}
	atomic.AddInt64(&g.deferred, 1)
	return false
}

// wait paces one admitted probe against its source's rate.
func (g *seriesProbeGate) wait(ctx context.Context) error {
	if g == nil || g.limiter == nil {
		return ctx.Err()
	}
	return g.limiter.Wait(ctx)
}

func (g *seriesProbeGate) deferredCount() int64 {
	if g == nil {
		return 0
	}
	return atomic.LoadInt64(&g.deferred)
}
