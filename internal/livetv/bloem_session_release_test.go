package livetv

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Only an active session is released: a second release, or one after the
// stale sweep, returns nil and leaves released_at where the first one put it.
func TestPgReleaseSessionReleasesOnlyAnActiveSession(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgStore(pool)
	tuner, err := store.CreateTuner(ctx, &Tuner{Type: TunerTypeHDHomeRun, DeviceID: uuid.NewString(), TunerCount: 2, Status: "ready", BaseURL: "http://192.168.1.2"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteTuner(ctx, tuner.ID) }()
	if err := store.ReplaceChannelsForTuner(ctx, tuner.ID, []Channel{{ID: uuid.NewString(), TunerID: tuner.ID, Number: "1", Name: "c", Enabled: true, StreamURL: "http://192.168.1.2/auto/v1"}}); err != nil {
		t.Fatal(err)
	}
	channels, err := store.ListChannels(ctx, tuner.ID)
	if err != nil || len(channels) != 1 {
		t.Fatalf("channels %v %v", channels, err)
	}

	released, err := store.CreateSession(ctx, SessionCreate{ChannelID: channels[0].ID, TunerID: tuner.ID, TunerIndex: 0})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ReleaseSession(ctx, released.ID)
	if err != nil || first == nil || first.Status != "released" || first.ReleasedAt == nil {
		t.Fatalf("first release = %+v, %v", first, err)
	}
	if again, err := store.ReleaseSession(ctx, released.ID); err != nil || again != nil {
		t.Fatalf("second release = %+v, %v; want nil", again, err)
	}
	if got, err := store.GetSession(ctx, released.ID); err != nil || got == nil || !got.ReleasedAt.Equal(*first.ReleasedAt) {
		t.Fatalf("after second release = %+v, %v; want released_at %s", got, err, first.ReleasedAt)
	}

	swept, err := store.CreateSession(ctx, SessionCreate{ChannelID: channels[0].ID, TunerID: tuner.ID, TunerIndex: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE livetv_sessions SET last_seen_at = now() - interval '2 hours' WHERE id = $1`, swept.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReleaseSessionsLastSeenBefore(ctx, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	sweptAt, err := store.GetSession(ctx, swept.ID)
	if err != nil || sweptAt == nil || sweptAt.Status != "released" {
		t.Fatalf("swept session = %+v, %v", sweptAt, err)
	}
	if again, err := store.ReleaseSession(ctx, swept.ID); err != nil || again != nil {
		t.Fatalf("release after the sweep = %+v, %v; want nil", again, err)
	}
	if got, err := store.GetSession(ctx, swept.ID); err != nil || got == nil || !got.ReleasedAt.Equal(*sweptAt.ReleasedAt) {
		t.Fatalf("after release following the sweep = %+v, %v; want released_at %s", got, err, sweptAt.ReleasedAt)
	}
}
