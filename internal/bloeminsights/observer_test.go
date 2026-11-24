package bloeminsights

import (
	"context"
	"testing"
	"time"

	evt "github.com/Silo-Server/silo-server/internal/events"
)

// The same lifecycle event can arrive twice (every replica publishes to
// every subscriber, and the event bus echoes); the observer keeps one row.
func TestObserverWritesOneRowForADuplicateStart(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	facts := NewMemoryFactsStore(time.Hour, nil)
	_ = facts.Record(context.Background(), "s9", RequestFacts{Proto: "HTTP/2.0", At: time.Now()})
	observer := NewObserver(store, nil, facts, nil)
	hub := evt.NewHub("test", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	ready := make(chan struct{})
	observer.ready = ready
	go func() { observer.Run(ctx, hub); close(done) }()
	<-ready
	for range 2 {
		if err := hub.PublishJSON(ctx, evt.ChannelSessions, "session.started", startedEvent("s9", 1), evt.PublishOptions{AdminOnly: true, EventID: "playback-lifecycle:1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := hub.PublishJSON(ctx, evt.ChannelCatalog, "catalog.updated", map[string]string{"x": "y"}, evt.PublishOptions{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM bloem_stream_records WHERE session_id = 's9'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rows = %d, want 1", count)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if observer.Applied() < 1 {
		t.Fatal("the observer applied nothing")
	}
	row, _ := readRecord(t, store, "s9")
	if row.viewerTransport != "tcp" || row.serverProto != "HTTP/2.0" {
		t.Fatalf("live row transport = %+v, want tcp from the facts", row)
	}
	cancel()
	<-done
}
