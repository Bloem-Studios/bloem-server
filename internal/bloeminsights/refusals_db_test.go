package bloeminsights

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type fakeLimitNotifier struct {
	mu      sync.Mutex
	notices []notifications.AdminPushNotice
}

func (f *fakeLimitNotifier) Notify(_ context.Context, n notifications.AdminPushNotice) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, n)
	return nil
}

func (f *fakeLimitNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.notices)
}

// A player hammering the stream limit gives one refusal row a minute per
// person, kind and scope, and one push.
func TestRefusalRecorderCoalescesAHammeringPlayer(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, username, email, password_hash, role) VALUES (9107, 'anna-refusals', 'anna-refusals@example.invalid', '', 'user') ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = 9107`) })
	notifier := &fakeLimitNotifier{}
	clock := time.Date(2026, 11, 24, 21, 0, 0, 0, time.UTC)
	recorder := NewRefusalRecorder(pool, notifier, func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{MaxStreams: 2}, nil
	}, nil)
	recorder.now = func() time.Time { return clock }
	for range 100 {
		recorder.record(ctx, refusalItem{userID: 9107, profileID: "p1", kind: playback.LimitStreams, scope: playback.ScopePerson})
		clock = clock.Add(500 * time.Millisecond)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bloem_limit_refusals WHERE user_id = 9107`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows after 100 refusals in 50 s = %d, want 1", rows)
	}
	if notifier.count() != 1 {
		t.Fatalf("pushes = %d, want 1", notifier.count())
	}
	n := notifier.notices[0]
	if n.Kind != notifications.AdminKindStreamLimitReached || n.Body != "anna-refusals was turned away: all 2 streams in use." {
		t.Fatalf("notice = %+v", n)
	}
	clock = clock.Add(61 * time.Second)
	recorder.record(ctx, refusalItem{userID: 9107, profileID: "p1", kind: playback.LimitStreams, scope: playback.ScopePerson})
	recorder.record(ctx, refusalItem{userID: 9107, profileID: "p1", kind: playback.LimitTranscodes, scope: playback.ScopePerson})
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bloem_limit_refusals WHERE user_id = 9107`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 3 {
		t.Fatalf("rows = %d, want a second stream row a minute later and a transcode row", rows)
	}
}

// LimitRefused never blocks the refused request: a full queue drops.
func TestRefusalRecorderNeverBlocks(t *testing.T) {
	recorder := NewRefusalRecorder(nil, nil, nil, nil)
	done := make(chan struct{})
	go func() {
		for range refusalQueueSize + 10 {
			recorder.LimitRefused(context.Background(), 1, "", playback.LimitStreams, playback.ScopePerson)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("LimitRefused blocked with no worker running")
	}
	if recorder.Dropped() == 0 {
		t.Fatal("a full queue dropped nothing")
	}
}
