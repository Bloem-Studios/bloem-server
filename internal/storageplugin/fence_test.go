package storageplugin

import (
	"errors"
	"testing"
)

// A host mutation that fails without committing a new generation lifts its
// fence: the same generation starts a fresh process instead of staying
// disabled until restart.
func TestFenceLiftRestartsTheUncommittedGeneration(t *testing.T) {
	m := manager(t)
	req := snapshot(t)
	resident, err := m.Ensure(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	lift := m.Fence(req.InstallationID)
	waitDone(t, resident)
	if _, err := m.Ensure(t.Context(), req); !errors.Is(err, ErrDisabled) {
		t.Fatalf("fenced generation borrowable during the mutation: %v", err)
	}
	lift()
	restored, err := m.Ensure(t.Context(), req)
	if err != nil {
		t.Fatal("lifted fence stayed disabled", err)
	}
	if restored == resident {
		t.Fatal("lifted fence reused the fenced process")
	}
	lift()
	if again, err := m.Ensure(t.Context(), req); err != nil || again != restored {
		t.Fatalf("spent lift disturbed the restarted process: %v", err)
	}
	m.Disable(req.InstallationID)
	waitDone(t, restored)
}

// Lifting is exact: it never reopens a fence that a committed mutation's
// Disable, a newer generation or a disabled snapshot has since replaced.
func TestFenceLiftNeverRevivesAReplacedSlot(t *testing.T) {
	m := manager(t)
	req := snapshot(t)
	req.Generation = 2
	resident, err := m.Ensure(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	lift := m.Fence(req.InstallationID)
	m.Disable(req.InstallationID)
	lift()
	waitDone(t, resident)
	if _, err := m.Ensure(t.Context(), req); !errors.Is(err, ErrDisabled) {
		t.Fatalf("lift reopened a committed fence: %v", err)
	}

	lift = m.Fence(req.InstallationID)
	req.Generation = 3
	newer, err := m.Ensure(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	lift()
	if again, err := m.Ensure(t.Context(), req); err != nil || again != newer {
		t.Fatalf("lift disturbed a newer generation: %v", err)
	}
	req.Generation = 2
	if _, err := m.Ensure(t.Context(), req); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("lift reopened a superseded generation: %v", err)
	}

	lift = m.Fence(req.InstallationID)
	waitDone(t, newer)
	req.Generation = 4
	req.Enabled = false
	if _, err := m.Ensure(t.Context(), req); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	lift()
	req.Enabled = true
	if _, err := m.Ensure(t.Context(), req); !errors.Is(err, ErrDisabled) {
		t.Fatalf("lift reopened a disabled snapshot's generation: %v", err)
	}
}
