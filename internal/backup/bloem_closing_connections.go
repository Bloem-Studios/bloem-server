package backup

import (
	"context"
	"time"
)

// Bloem: a connection that was just closed, such as the one ServerID opens
// to read the live server's identity right before the swap preflight, stays
// in pg_stat_activity until its server process exits. On a loaded host that
// takes long enough for the connection count to see it and refuse the
// restore with "1 other clients are connected". Before Silo's count,
// bloemAwaitClosingConnections waits up to bloemClosingConnectionGrace for
// such sessions to end. A client that stays connected is still refused, by
// Silo's own check right after.
var bloemClosingConnectionGrace = 2 * time.Second

const bloemClosingConnectionPoll = 25 * time.Millisecond

// bloemAwaitClosingConnections returns once no other session is on live, on
// a counting error (Silo's check reports it), when ctx ends, or when the
// grace has passed.
func bloemAwaitClosingConnections(ctx context.Context, ctl ControlDB, live string) {
	deadline := time.Now().Add(bloemClosingConnectionGrace)
	for {
		others, err := ctl.OtherConnections(ctx, live)
		if err != nil || others == 0 || !time.Now().Before(deadline) {
			return
		}
		timer := time.NewTimer(bloemClosingConnectionPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
