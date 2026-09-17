package livetv

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func setLastSeen(store *memoryStore, id string, at time.Time) {
	store.mu.Lock()
	defer store.mu.Unlock()
	row := store.sessions[id]
	row.LastSeenAt = at
	store.sessions[id] = row
}

func lastSeen(store *memoryStore, id string) time.Time {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.sessions[id].LastSeenAt
}

// The first renewal happens before HoldSessionLease returns, so a proxy that
// starts copying already holds a fresh lease.
func TestHoldSessionLeaseRenewsSynchronously(t *testing.T) {
	svc, store := singleTunerService(t)
	ctx := context.Background()
	session, err := svc.StartChannelSession(ctx, "ch1", 7, "profile-1", ClientCapabilities{})
	if err != nil {
		t.Fatalf("StartChannelSession: %v", err)
	}
	stale := time.Now().Add(-2 * StaleSessionTTL)
	setLastSeen(store, session.ID, stale)

	leaseCtx, stop, err := svc.holdSessionLease(ctx, session.ID, time.Hour)
	if err != nil {
		t.Fatalf("holdSessionLease: %v", err)
	}
	defer stop()
	if !lastSeen(store, session.ID).After(stale) {
		t.Fatal("lease returned before renewing last_seen_at")
	}
	if leaseCtx.Err() != nil {
		t.Fatal("lease context canceled for an active session")
	}
}

// An MPEG-TS proxy makes one request and then copies for as long as the viewer
// watches. It used to touch the lease once, so an active stream was reclaimed
// after StaleSessionTTL and its tuner handed to someone else mid-playback.
func TestHoldSessionLeaseRenewsWhileStreaming(t *testing.T) {
	svc, store := singleTunerService(t)
	ctx := context.Background()
	session, err := svc.StartChannelSession(ctx, "ch1", 7, "profile-1", ClientCapabilities{})
	if err != nil {
		t.Fatalf("StartChannelSession: %v", err)
	}
	var clock atomic.Int64
	base := time.Now()
	svc.now = func() time.Time { return base.Add(time.Duration(clock.Load())) }
	_, stop, err := svc.holdSessionLease(ctx, session.ID, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("holdSessionLease: %v", err)
	}
	defer stop()

	// Age the row while renewals are still throttled, then step the clock past
	// the throttle and let a periodic renewal refresh it.
	stale := time.Now().Add(-2 * StaleSessionTTL)
	setLastSeen(store, session.ID, stale)
	clock.Store(int64(time.Hour))
	waitFor(t, "periodic renewal", func() bool { return lastSeen(store, session.ID).After(stale) })
}

func TestHoldSessionLeaseFailsClosed(t *testing.T) {
	svc, store := singleTunerService(t)
	ctx := context.Background()

	leaseCtx, stop, err := svc.HoldSessionLease(ctx, "missing")
	defer stop()
	if !errors.Is(err, ErrNotFound) || leaseCtx.Err() == nil {
		t.Fatalf("missing session: err=%v ctxErr=%v, want ErrNotFound and a canceled context", err, leaseCtx.Err())
	}

	session, err := svc.StartChannelSession(ctx, "ch1", 7, "profile-1", ClientCapabilities{})
	if err != nil {
		t.Fatalf("StartChannelSession: %v", err)
	}
	if _, err := store.ReleaseSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	_, releasedStop, err := svc.HoldSessionLease(ctx, session.ID)
	defer releasedStop()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("released session: err=%v, want ErrNotFound", err)
	}

	var nilSvc *Service
	nilCtx, nilStop, err := nilSvc.HoldSessionLease(ctx, "x")
	defer nilStop()
	if err == nil || nilCtx.Err() == nil {
		t.Fatal("nil service granted a lease")
	}
}

// Once a session is released -- by its owner, an admin, or the reclaim on any
// replica -- the proxy must stop pulling from the tuner the database now
// considers free.
func TestHoldSessionLeaseEndsWhenSessionReleased(t *testing.T) {
	svc, _ := singleTunerService(t)
	ctx := context.Background()
	session, err := svc.StartChannelSession(ctx, "ch1", 7, "profile-1", ClientCapabilities{})
	if err != nil {
		t.Fatalf("StartChannelSession: %v", err)
	}
	leaseCtx, stop, err := svc.holdSessionLease(ctx, session.ID, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("holdSessionLease: %v", err)
	}
	defer stop()

	if _, err := svc.ReleaseSession(ctx, session.ID, 0, "", false); err != nil {
		t.Fatalf("ReleaseSession: %v", err)
	}
	select {
	case <-leaseCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("lease context still open after the session was released")
	}
}

// failingSessionStore fails GetSession on demand and records the deadline each
// call ran under.
type failingSessionStore struct {
	*memoryStore
	fail        atomic.Bool
	sawDeadline atomic.Bool
}

func (s *failingSessionStore) GetSession(ctx context.Context, id string) (*LiveSession, error) {
	if _, ok := ctx.Deadline(); ok {
		s.sawDeadline.Store(true)
	}
	if s.fail.Load() {
		return nil, errors.New("database unavailable")
	}
	return s.memoryStore.GetSession(ctx, id)
}

// A store blip must not cut a healthy stream, but a stream the store cannot
// confirm for StaleSessionTTL is no longer known to hold its tuner.
func TestHoldSessionLeaseBoundsStoreFailures(t *testing.T) {
	_, mem := singleTunerService(t)
	store := &failingSessionStore{memoryStore: mem}
	svc := NewServiceWithStore(store)
	ctx := context.Background()
	session, err := svc.StartChannelSession(ctx, "ch1", 7, "profile-1", ClientCapabilities{})
	if err != nil {
		t.Fatalf("StartChannelSession: %v", err)
	}
	var clock atomic.Int64
	base := time.Now()
	svc.now = func() time.Time { return base.Add(time.Duration(clock.Load())) }

	leaseCtx, stop, err := svc.holdSessionLease(ctx, session.ID, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("holdSessionLease: %v", err)
	}
	defer stop()
	if !store.sawDeadline.Load() {
		t.Fatal("lease store call ran without a deadline")
	}

	store.fail.Store(true)
	time.Sleep(30 * time.Millisecond)
	if leaseCtx.Err() != nil {
		t.Fatal("a transient store error ended the stream")
	}
	clock.Store(int64(StaleSessionTTL + time.Second))
	select {
	case <-leaseCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("lease survived StaleSessionTTL without store confirmation")
	}
}

func TestOwnerMatchesRequiresRecordedProfile(t *testing.T) {
	if ownerMatches(7, "p1", 7, "") {
		t.Fatal("a caller without a profile reached a profile-scoped row")
	}
	if !ownerMatches(7, "", 7, "") || !ownerMatches(7, "", 7, "p2") {
		t.Fatal("an account-scoped row must stay visible to its account")
	}
}
