package livetv

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// liveProcess is the part of a running live HLS remux the bridge manages.
// *playback.LiveHLSSession satisfies it.
type liveProcess interface {
	Done() <-chan struct{}
	Err() error
	Close() error
}

// defaultBridgeCleanupDelay lets in-flight segment fetches finish before a
// session directory is removed.
const defaultBridgeCleanupDelay = 2 * time.Second

func (b *HLSBridge) setLedger(ledger bridgeLedger) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ledger = ledger
}

func (b *HLSBridge) currentLedger() bridgeLedger {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ledger
}

// trackSession registers a started remux and supervises it until it ends.
func (b *HLSBridge) trackSession(id string, sess *bridgeSession) {
	b.mu.Lock()
	b.sessions[id] = sess
	b.mu.Unlock()
	go b.superviseSession(id, sess)
}

// superviseSession ties a remux to its durable session row in both directions.
//
// When ffmpeg exits on its own, the remux's slot and directory are freed and
// the session row is released at once, so the tuner does not sit claimed for
// StaleSessionTTL. Before this a dead remux stayed registered and a client
// polling its frozen playlist kept renewing the lease indefinitely.
//
// While ffmpeg runs, the row is checked every leaseCheckInterval. A release can
// land on any replica, but only this replica can stop this process, so a row
// that is released or missing ends the remux here. Store errors are tolerated
// for StaleSessionTTL -- the same bound the database applies to an unrenewed
// session -- before the remux is stopped as unconfirmable.
func (b *HLSBridge) superviseSession(id string, sess *bridgeSession) {
	var tick <-chan time.Time
	if b.leaseCheckInterval > 0 {
		ticker := time.NewTicker(b.leaseCheckInterval)
		defer ticker.Stop()
		tick = ticker.C
	}
	lastConfirmed := time.Now()
	for {
		select {
		case <-sess.live.Done():
			if !b.forgetSession(id, sess) {
				// StopLiveStream or a lease loss already owns the teardown.
				return
			}
			slog.Warn("livetv hls bridge remux exited; releasing session",
				"playback_session_id", id, "error", sess.live.Err())
			b.releaseSession(sess)
			if ledger := b.currentLedger(); ledger != nil {
				ctx, cancel := context.WithTimeout(context.Background(), sessionLeaseCallTimeout)
				ledger.releaseBridgeSession(ctx, id)
				cancel()
			}
			return
		case <-tick:
			ledger := b.currentLedger()
			if ledger == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), sessionLeaseCallTimeout)
			active, err := ledger.bridgeSessionActive(ctx, id)
			cancel()
			if err == nil && active {
				lastConfirmed = time.Now()
				continue
			}
			if err != nil && time.Since(lastConfirmed) < StaleSessionTTL {
				slog.Warn("livetv hls bridge session check failed",
					"playback_session_id", id, "error", err)
				continue
			}
			slog.Info("livetv hls bridge session no longer active; stopping remux",
				"playback_session_id", id, "error", err)
			if b.forgetSession(id, sess) {
				b.releaseSession(sess)
			}
			return
		}
	}
}

// forgetSession removes id from the bridge if it still maps to sess. It reports
// whether this caller removed it, so exactly one path tears a session down.
func (b *HLSBridge) forgetSession(id string, sess *bridgeSession) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if current, ok := b.sessions[id]; !ok || current != sess {
		return false
	}
	delete(b.sessions, id)
	return true
}

// releaseSession frees everything one bridge session holds.
func (b *HLSBridge) releaseSession(sess *bridgeSession) {
	b.releaseTranscodeSlot(sess.holdsTranscodeSlot)
	_ = sess.live.Close()
	if b.cleanupDelay <= 0 {
		_ = os.RemoveAll(sess.dir)
		return
	}
	go func(dir string, delay time.Duration) {
		time.Sleep(delay)
		_ = os.RemoveAll(dir)
	}(sess.dir, b.cleanupDelay)
}

// lookupLiveSession returns a session whose remux is still running. A session
// whose ffmpeg has exited is treated as gone even before the supervisor runs, so
// a request cannot renew the lease of a stream that will never advance.
func (b *HLSBridge) lookupLiveSession(id string) *bridgeSession {
	b.mu.Lock()
	sess := b.sessions[id]
	b.mu.Unlock()
	if sess == nil {
		return nil
	}
	select {
	case <-sess.live.Done():
		return nil
	default:
		return sess
	}
}
