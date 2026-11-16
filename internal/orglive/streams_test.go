package orglive

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/events"
)

type owners map[string]uuid.UUID // "account/profile" -> organization

func (o owners) SessionOrganization(_ context.Context, account int, profile string) (uuid.UUID, error) {
	return o[fmt.Sprintf("%d/%s", account, profile)], nil
}

type fingerprints struct {
	mu   sync.Mutex
	sets map[uuid.UUID]string
	err  error
}

func (f *fingerprints) set(org uuid.UUID, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets[org] = value
}

func (f *fingerprints) StreamsFingerprint(_ context.Context, org uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sets[org], f.err
}

type bridgeFixture struct {
	hub          *events.Hub
	bus          *Bus
	orgA, orgB   uuid.UUID
	a, b         *Mailbox
	fingerprints *fingerprints
}

func newBridgeFixture(t *testing.T) *bridgeFixture {
	t.Helper()
	f := &bridgeFixture{hub: events.NewHub("node-1", nil), bus: healthyBus(), orgA: uuid.New(), orgB: uuid.New(), fingerprints: &fingerprints{sets: map[uuid.UUID]string{}}}
	f.a, f.b = subscribed(ChannelStreams), subscribed(ChannelStreams)
	if _, err := f.bus.Register(f.orgA, 1, f.a); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bus.Register(f.orgB, 2, f.b); err != nil {
		t.Fatal(err)
	}
	bridge := NewStreamsBridge(f.hub, f.bus, owners{
		"10/p1": f.orgA, // an account in organization A
		"20/p2": f.orgB, // an account in organization B
		// 30/p3 belongs to no organization
	}, f.fingerprints, nil)
	bridge.delay = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); bridge.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	<-bridge.Ready()
	return f
}

func lifecycleEnvelope(t *testing.T, event, session string, seq int64, owner map[string]any) events.Envelope {
	t.Helper()
	data := map[string]any{"session_id": session, "seq": seq, "state": "playing"}
	for key, value := range owner {
		data[key] = value
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return events.Envelope{Channel: events.ChannelSessions, Event: event, Data: raw, AdminOnly: true}
}

func liveSession(account int, profile string) map[string]any {
	return map[string]any{"session": map[string]any{"user_id": account, "profile_id": profile}}
}

func waitFrames(t *testing.T, m *Mailbox, want int) []Frame {
	t.Helper()
	var frames []Frame
	deadline := time.After(2 * time.Second)
	for len(frames) < want {
		select {
		case <-m.Wake():
			frames = append(frames, m.Drain()...)
		case <-deadline:
			t.Fatalf("got %d frames %+v, want %d", len(frames), frames, want)
		}
	}
	return frames
}

func expectQuiet(t *testing.T, m *Mailbox, label string) {
	t.Helper()
	select {
	case <-m.Wake():
		if frames := m.Drain(); len(frames) != 0 {
			t.Fatalf("%s received %+v", label, frames)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

func TestStreamsBridgeSignalsOnlyTheOwningOrganization(t *testing.T) {
	f := newBridgeFixture(t)
	ctx := t.Context()

	// session.started for an account in organization A.
	if err := f.hub.Publish(ctx, lifecycleEnvelope(t, EventSessionStarted, "sess-a", 1, liveSession(10, "p1"))); err != nil {
		t.Fatal(err)
	}
	frames := waitFrames(t, f.a, 1)
	if got := frames[0]; got.Resync || got.Channel != ChannelStreams || got.Change.ID != "sess-a" || got.Change.Kind != KindCreated || got.Change.Rev != "1" {
		t.Fatalf("organization A got %+v", got)
	}
	expectQuiet(t, f.b, "organization B")

	// A pause and a stop follow, the stop carrying only the history entry.
	_ = f.hub.Publish(ctx, lifecycleEnvelope(t, EventSessionPaused, "sess-a", 2, liveSession(10, "p1")))
	if got := waitFrames(t, f.a, 1)[0].Change; got.Kind != KindUpdated || got.Rev != "2" {
		t.Fatalf("pause: %+v", got)
	}
	_ = f.hub.Publish(ctx, lifecycleEnvelope(t, EventSessionStopped, "sess-a", 3, map[string]any{"history": map[string]any{"user_id": "10", "profile_id": "p1"}}))
	if got := waitFrames(t, f.a, 1)[0].Change; got.Kind != KindDeleted || got.Rev != "3" {
		t.Fatalf("stop: %+v", got)
	}
	expectQuiet(t, f.b, "organization B")

	// Organization B's own start reaches B and not A.
	_ = f.hub.Publish(ctx, lifecycleEnvelope(t, EventSessionStarted, "sess-b", 1, liveSession(20, "p2")))
	frames = waitFrames(t, f.b, 1)
	if frames[0].Change.ID != "sess-b" {
		t.Fatalf("organization B got %+v", frames)
	}
	expectQuiet(t, f.a, "organization A")
}

func TestStreamsBridgeNeverBroadcastsASessionWithoutAnOrganization(t *testing.T) {
	f := newBridgeFixture(t)
	f.fingerprints.set(f.orgA, "same")
	f.fingerprints.set(f.orgB, "same")
	ctx := t.Context()

	// An account in no organization, a profile outside the organization, and a
	// payload with no owner at all.
	_ = f.hub.Publish(ctx, lifecycleEnvelope(t, EventSessionStarted, "orphan", 1, liveSession(30, "p3")))
	_ = f.hub.Publish(ctx, lifecycleEnvelope(t, EventSessionStarted, "wrong-profile", 1, liveSession(10, "p-other-org")))
	// Neither is attributed to an organization, so no change frame reaches
	// either: the only thing that may happen is a comparison, and that only
	// ever sends a resync, never a session id.
	deadline := time.After(300 * time.Millisecond)
	for done := false; !done; {
		select {
		case <-f.a.Wake():
			for _, frame := range f.a.Drain() {
				if !frame.Resync {
					t.Fatalf("organization A got a change for an unattributed session: %+v", frame)
				}
			}
		case <-f.b.Wake():
			for _, frame := range f.b.Drain() {
				if !frame.Resync {
					t.Fatalf("organization B got a change for an unattributed session: %+v", frame)
				}
			}
		case <-deadline:
			done = true
		}
	}
}

func TestStreamsBridgeComparesWhenAnEventNamesNoSession(t *testing.T) {
	f := newBridgeFixture(t)
	f.fingerprints.set(f.orgA, "a1")
	f.fingerprints.set(f.orgB, "b1")
	ctx := t.Context()
	replaced := events.Envelope{Channel: events.ChannelSessions, Event: EventSessionsChanged, AdminOnly: true}

	// The first look has nothing to compare with, so both organizations re-read.
	_ = f.hub.Publish(ctx, replaced)
	for label, m := range map[string]*Mailbox{"A": f.a, "B": f.b} {
		if frames := waitFrames(t, m, 1); !frames[0].Resync || frames[0].Channel != ChannelStreams {
			t.Fatalf("organization %s first look: %+v", label, frames)
		}
	}

	// Nothing changed: nothing is sent.
	_ = f.hub.Publish(ctx, replaced)
	expectQuiet(t, f.a, "organization A")
	expectQuiet(t, f.b, "organization B")

	// Only organization A's stream set changed.
	f.fingerprints.set(f.orgA, "a2")
	_ = f.hub.Publish(ctx, replaced)
	if frames := waitFrames(t, f.a, 1); !frames[0].Resync {
		t.Fatalf("organization A: %+v", frames)
	}
	expectQuiet(t, f.b, "organization B")
}

func TestStreamsBridgeIgnoresOtherChannelsAndEvents(t *testing.T) {
	f := newBridgeFixture(t)
	ctx := t.Context()
	other := lifecycleEnvelope(t, EventSessionStarted, "x", 1, liveSession(10, "p1"))
	other.Channel = events.ChannelTasks
	_ = f.hub.Publish(ctx, other)
	_ = f.hub.Publish(ctx, lifecycleEnvelope(t, "session.progress", "x", 1, liveSession(10, "p1")))
	expectQuiet(t, f.a, "organization A")
	expectQuiet(t, f.b, "organization B")
}

func TestStreamsBridgeDoesNothingWithoutSubscribers(t *testing.T) {
	hub, bus := events.NewHub("node-1", nil), healthyBus()
	calls := 0
	bridge := NewStreamsBridge(hub, bus, ownersFunc(func(context.Context, int, string) (uuid.UUID, error) { calls++; return uuid.New(), nil }), nil, nil)
	env := lifecycleEnvelope(t, EventSessionStarted, "s", 1, liveSession(10, "p1"))
	if bridge.handle(t.Context(), env) {
		t.Fatal("an event with no subscriber asked for a comparison")
	}
	if calls != 0 {
		t.Fatalf("looked up an owner %d times with nobody listening", calls)
	}
}

type ownersFunc func(context.Context, int, string) (uuid.UUID, error)

func (f ownersFunc) SessionOrganization(ctx context.Context, account int, profile string) (uuid.UUID, error) {
	return f(ctx, account, profile)
}

func TestStreamsBridgeStartRunsOnce(t *testing.T) {
	bridge := NewStreamsBridge(events.NewHub("node-1", nil), healthyBus(), owners{}, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); bridge.Start(ctx) }()
	}
	wg.Wait()
	select {
	case <-bridge.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("the bridge never started")
	}
	// A second Run would close Ready twice and panic.
	bridge.Start(ctx)
	<-time.After(30 * time.Millisecond) // a second Run would have panicked by now
}
