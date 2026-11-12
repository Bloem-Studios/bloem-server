package libraryingest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A scan's budget admits exactly perScan new episode probes; the rest wait for
// a later scan. Zero means no budget.
func TestSeriesProbeGateBudget(t *testing.T) {
	c := &StorageScanner{}
	if err := c.ConfigureNativeSeriesProbes(2, 0, 0); err != nil {
		t.Fatal(err)
	}
	gate := c.newSeriesProbeGate(uuid.New())
	var admitted []bool
	for range 4 {
		admitted = append(admitted, gate.admit())
	}
	if !slices.Equal(admitted, []bool{true, true, false, false}) {
		t.Fatalf("budget of two admitted %v", admitted)
	}
	if gate.deferred != 2 {
		t.Fatalf("deferred = %d", gate.deferred)
	}
	unlimited := (&StorageScanner{}).newSeriesProbeGate(uuid.New())
	for range 100 {
		if !unlimited.admit() {
			t.Fatal("unbudgeted scan deferred a probe")
		}
	}
	if err := c.ConfigureNativeSeriesProbes(-1, 0, 0); err == nil {
		t.Fatal("negative budget accepted")
	}
}

// Probes of one source share one rate across scans; another source is not
// slowed by it, and cancellation ends a wait.
func TestSeriesProbeGateRate(t *testing.T) {
	c := &StorageScanner{}
	if err := c.ConfigureNativeSeriesProbes(0, 20, 1); err != nil {
		t.Fatal(err)
	}
	source := uuid.New()
	first, second := c.newSeriesProbeGate(source), c.newSeriesProbeGate(source)
	started := time.Now()
	for _, gate := range []*seriesProbeGate{first, second, first} {
		if err := gate.wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// Three probes at 20 per second with a burst of one take at least 100 ms.
	if elapsed := time.Since(started); elapsed < 90*time.Millisecond {
		t.Fatalf("three probes took %v", elapsed)
	}
	other := c.newSeriesProbeGate(uuid.New())
	started = time.Now()
	if err := other.wait(t.Context()); err != nil || time.Since(started) > 40*time.Millisecond {
		t.Fatalf("another source waited %v: %v", time.Since(started), err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := first.wait(canceled); err == nil {
		t.Fatal("canceled wait succeeded")
	}
}
