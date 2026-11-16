package orglive

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeConn is a notifyConn the test drives: payloads and failures arrive on
// script, and Wait honors its context like a real connection.
type fakeConn struct {
	script  chan any // string payload, or error
	pingErr error
	mu      sync.Mutex
	pings   int
	closed  bool
}

func newFakeConn() *fakeConn { return &fakeConn{script: make(chan any, 16)} }

func (c *fakeConn) Listen(context.Context, string) error { return nil }

func (c *fakeConn) Wait(ctx context.Context) (string, error) {
	select {
	case item := <-c.script:
		if err, ok := item.(error); ok {
			return "", err
		}
		return item.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (c *fakeConn) Ping(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pings++
	return c.pingErr
}

func (c *fakeConn) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeConn) pingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pings
}

func startListener(t *testing.T, dial func(context.Context) (notifyConn, error)) (*Bus, *Listener, func()) {
	t.Helper()
	bus := NewBus()
	listener := newListener(bus, dial, nil)
	listener.minBackoff, listener.maxBackoff, listener.probe = 5*time.Millisecond, 20*time.Millisecond, 30*time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); listener.Run(ctx) }()
	return bus, listener, func() { cancel(); <-done }
}

// waitFor polls cond until it holds, failing the test after a few seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

func waitHealthy(t *testing.T, bus *Bus, want bool) {
	t.Helper()
	waitFor(t, fmt.Sprintf("the bus to be healthy=%v", want), func() bool { return bus.Healthy() == want })
}

func payload(channel string, org uuid.UUID, id, kind string) string {
	return fmt.Sprintf(`{"ch":%q,"org":%q,"id":%q,"rev":"1","kind":%q}`, channel, org, id, kind)
}

func TestListenerDeliversOnlyToTheNamedOrganization(t *testing.T) {
	conn := newFakeConn()
	bus, _, stop := startListener(t, func(context.Context) (notifyConn, error) { return conn, nil })
	defer stop()
	waitHealthy(t, bus, true)

	orgA, orgB := uuid.New(), uuid.New()
	a, b := subscribed(ChannelPeople, ChannelActivity), subscribed(ChannelPeople, ChannelActivity)
	if _, err := bus.Register(orgA, 1, a); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Register(orgB, 2, b); err != nil {
		t.Fatal(err)
	}

	conn.script <- payload("org.people", orgA, "7", "updated")
	conn.script <- "not a payload"                                   // dropped
	conn.script <- payload("org.streams", orgA, "s1", "created")     // streams never come from the database
	conn.script <- payload("org.activity", uuid.Nil, "x", "created") // no organization: dropped
	conn.script <- payload("org.activity", orgB, "lifecycle:9", "created")

	frames := waitFrames(t, a, 1)
	if frames[0].Change.ID != "7" || frames[0].Channel != ChannelPeople {
		t.Fatalf("organization A got %+v", frames)
	}
	frames = waitFrames(t, b, 1)
	if frames[0].Change.ID != "lifecycle:9" || frames[0].Channel != ChannelActivity {
		t.Fatalf("organization B got %+v", frames)
	}
	expectQuiet(t, a, "organization A")
	expectQuiet(t, b, "organization B")
}

func TestListenerGoingDownClosesSocketsAndRecovers(t *testing.T) {
	first, second := newFakeConn(), newFakeConn()
	var mu sync.Mutex
	dials := 0
	bus, _, stop := startListener(t, func(context.Context) (notifyConn, error) {
		mu.Lock()
		defer mu.Unlock()
		dials++
		switch dials {
		case 1:
			return first, nil
		case 2:
			return nil, errors.New("database unreachable")
		default:
			return second, nil
		}
	})
	defer stop()
	waitHealthy(t, bus, true)

	m := subscribed(ChannelPeople)
	if _, err := bus.Register(uuid.New(), 1, m); err != nil {
		t.Fatal(err)
	}
	first.script <- errors.New("connection reset")
	select {
	case <-m.Done():
		if m.Reason() != ReasonFeedDown {
			t.Fatalf("reason = %v, want feed down", m.Reason())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a socket stayed open after the change feed dropped")
	}
	if !first.closed {
		t.Fatal("the failed connection was not closed")
	}
	// While down, no socket may register; once the feed is back, one can.
	waitHealthy(t, bus, true)
	if _, err := bus.Register(uuid.New(), 1, NewMailbox(0)); err != nil {
		t.Fatalf("after recovery: %v", err)
	}
	org := uuid.New()
	n := subscribed(ChannelPeople)
	if _, err := bus.Register(org, 3, n); err != nil {
		t.Fatal(err)
	}
	second.script <- payload("org.people", org, "5", "created")
	if frames := waitFrames(t, n, 1); frames[0].Change.ID != "5" {
		t.Fatalf("after recovery got %+v", frames)
	}
}

func TestListenerProbesQuietConnections(t *testing.T) {
	healthy, dead := newFakeConn(), newFakeConn()
	dead.pingErr = errors.New("broken pipe")
	var mu sync.Mutex
	dials := 0
	bus, _, stop := startListener(t, func(context.Context) (notifyConn, error) {
		mu.Lock()
		defer mu.Unlock()
		dials++
		if dials == 1 {
			return dead, nil
		}
		return healthy, nil
	})
	defer stop()
	waitHealthy(t, bus, true)
	// A quiet connection that fails its probe is replaced...
	waitFor(t, "the dead connection to be replaced", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return dials >= 2
	})
	waitHealthy(t, bus, true)
	// ...and one that answers is kept: probes pass and the feed stays up.
	waitFor(t, "the healthy connection to be probed", func() bool { return healthy.pingCount() > 0 })
	if !bus.Healthy() {
		t.Fatal("a healthy quiet connection was dropped")
	}
}

func TestListenerStoppingShutsTheBusDown(t *testing.T) {
	conn := newFakeConn()
	bus, _, stop := startListener(t, func(context.Context) (notifyConn, error) { return conn, nil })
	waitHealthy(t, bus, true)
	m := subscribed(ChannelPeople)
	if _, err := bus.Register(uuid.New(), 1, m); err != nil {
		t.Fatal(err)
	}
	stop()
	if m.Reason() != ReasonShutdown || bus.Healthy() {
		t.Fatalf("after stop: reason %v, healthy %v", m.Reason(), bus.Healthy())
	}
}

// Start brings the listener up once however often and however concurrently it
// is called: a node opens one LISTEN connection, and only when something asks.
func TestListenerStartRunsOnce(t *testing.T) {
	var mu sync.Mutex
	dials := 0
	bus := NewBus()
	listener := newListener(bus, func(context.Context) (notifyConn, error) {
		mu.Lock()
		defer mu.Unlock()
		dials++
		return newFakeConn(), nil
	}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if bus.Healthy() {
		t.Fatal("a listener that was never started made the bus healthy")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); listener.Start(ctx) }()
	}
	wg.Wait()
	waitHealthy(t, bus, true)
	listener.Start(ctx)
	<-time.After(50 * time.Millisecond) // a second dial, if there were one, would have happened by now
	mu.Lock()
	defer mu.Unlock()
	if dials != 1 {
		t.Fatalf("dialed %d times, want 1", dials)
	}
}
