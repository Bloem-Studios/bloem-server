//go:build integration

package handlers

import (
	"context"
	"sync/atomic"
)

// Silo removed these fakes with its own library-monitor tests (#2817); Bloem's
// library-location tests still use them.

type countingMonitorPoker struct{ pokes atomic.Int32 }

func (p *countingMonitorPoker) Poke() { p.pokes.Add(1) }

type noopLibraryScanQueue struct{}

func (noopLibraryScanQueue) EnqueueLibraryScan(context.Context, int, string) (bool, error) {
	return true, nil
}

func (noopLibraryScanQueue) EnqueueScan(context.Context, int, string, string, string) (bool, error) {
	return true, nil
}

func (noopLibraryScanQueue) CancelAcceptedByLibrary(context.Context, int) (int, error) { return 0, nil }

func (noopLibraryScanQueue) CancelByLibrary(context.Context, int) (int, error) { return 0, nil }
