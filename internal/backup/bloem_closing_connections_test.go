package backup

import (
	"context"
	"strings"
	"testing"
	"time"
)

// closingControl reports a session on the live database for the first
// closingCalls counts, as pg_stat_activity does for a connection the
// command just closed whose server process has not exited yet.
type closingControl struct {
	*fakeControl
	closingCalls int
	counted      int
}

func (c *closingControl) OtherConnections(ctx context.Context, name string) (int, error) {
	c.counted++
	if name == liveDB && c.counted <= c.closingCalls {
		return 1, nil
	}
	return c.fakeControl.OtherConnections(ctx, name)
}

func noExtensions() ([]string, error) { return nil, nil }

func TestBloemSwapPreflightWaitsForAClosingConnection(t *testing.T) {
	ctl := &closingControl{fakeControl: newFakeControl(t), closingCalls: 3}
	if _, err := swapPreflight(t.Context(), ctl, nil, liveDB, "silo_restore_x", "silo_before_restore_x", noExtensions); err != nil {
		t.Fatalf("a connection that is still closing stopped the restore: %v", err)
	}
}

func TestBloemLiveIdleCheckWaitsForAClosingConnection(t *testing.T) {
	ctl := &closingControl{fakeControl: newFakeControl(t), closingCalls: 3}
	if err := checkLiveIdle(t.Context(), ctl, liveDB); err != nil {
		t.Fatalf("a connection that is still closing stopped the restore: %v", err)
	}
}

func TestBloemSwapPreflightStillRefusesAConnectedClient(t *testing.T) {
	grace := bloemClosingConnectionGrace
	bloemClosingConnectionGrace = 100 * time.Millisecond
	t.Cleanup(func() { bloemClosingConnectionGrace = grace })
	ctl := newFakeControl(t)
	ctl.dbs[liveDB].sessions = 1
	_, err := swapPreflight(t.Context(), ctl, nil, liveDB, "silo_restore_x", "silo_before_restore_x", noExtensions)
	if err == nil || !strings.Contains(err.Error(), "1 other clients are connected") {
		t.Fatalf("swapPreflight with a client that stays connected = %v, want the refusal", err)
	}
	if err := checkLiveIdle(t.Context(), ctl, liveDB); err == nil {
		t.Fatal("checkLiveIdle let a client that stays connected through")
	}
}
