package pgstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/storetest"
)

type playbackSinkFixture struct {
	store *PostgresUserStore
	pool  *pgxpool.Pool
	app   string
	scope userstore.PlaybackProgressScope
	fence userstore.PlaybackProgressFence
}

func newPlaybackSinkFixture(t *testing.T) playbackSinkFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	app := fmt.Sprintf("playback-sink-%d", time.Now().UnixNano())
	config.ConnConfig.RuntimeParams["application_name"] = app
	config.MaxConns = 6
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var userID int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,role) VALUES($1,'user') RETURNING id`, app).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"playback_progress_sinks", "user_watch_history", "user_watch_progress", "user_history_hidden_items", "user_profiles", "users"} {
			column := "user_id"
			if table == "users" {
				column = "id"
			}
			if _, err := pool.Exec(context.Background(), "DELETE FROM "+pgx.Identifier{table}.Sanitize()+" WHERE "+column+"=$1", userID); err != nil {
				t.Error(err)
			}
		}
	})
	return playbackSinkFixture{store: newStore(pool, userID), pool: pool, app: app, scope: userstore.PlaybackProgressScope{ProfileID: "p", SessionID: "session", MediaItemID: "movie"}, fence: userstore.PlaybackProgressFence{AttemptID: "attempt", Incarnation: "incarnation", OwnerID: "boot", Epoch: 1}}
}

func (f playbackSinkFixture) install(t *testing.T) {
	t.Helper()
	if _, err := f.store.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: f.scope, Next: f.fence}); err != nil {
		t.Fatal(err)
	}
}

func playbackSinkSample(sequence int64, position float64) userstore.PlaybackProgressSample {
	return userstore.PlaybackProgressSample{Sequence: sequence, PositionSeconds: position, DurationSeconds: 100, Thresholds: userstore.ProgressThresholds{MinResumePct: 1, WatchedPct: 90}, Hints: userstore.VersionHints{FileID: 2, Resolution: "1080p"}}
}

func TestPostgresPlaybackSinkConformance(t *testing.T) {
	storetest.PlaybackSink(t, func(t *testing.T) userstore.UserStore { return newPlaybackSinkFixture(t).store })
}

func (f playbackSinkFixture) snapshot(t *testing.T) string {
	t.Helper()
	var snapshot string
	err := f.pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'sink',(SELECT jsonb_agg(to_jsonb(s)) FROM playback_progress_sinks s WHERE user_id=$1),
		'progress',(SELECT jsonb_agg(to_jsonb(p)) FROM user_watch_progress p WHERE user_id=$1),
		'history',(SELECT jsonb_agg(to_jsonb(h)) FROM user_watch_history h WHERE user_id=$1))::text`, f.store.userID).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestPostgresPlaybackSinkRollbackAtEveryWrite(t *testing.T) {
	for _, stage := range []string{"progress", "hints", "history", "receipt"} {
		t.Run(stage, func(t *testing.T) {
			f := newPlaybackSinkFixture(t)
			f.install(t)
			if _, err := f.store.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, Sample: playbackSinkSample(1, 20)}); err != nil {
				t.Fatal(err)
			}
			before := f.snapshot(t)
			table := "user_watch_progress"
			condition := fmt.Sprintf("NEW.user_id=%d", f.store.userID)
			switch stage {
			case "hints":
				condition += " AND NEW.last_file_id=3"
			case "history":
				table = "user_watch_history"
			case "receipt":
				table = "playback_progress_sinks"
			}
			name := fmt.Sprintf("sink_fault_%d", f.store.userID)
			function := pgx.Identifier{name}.Sanitize()
			if _, err := f.pool.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF %s THEN RAISE EXCEPTION 'injected sink failure'; END IF; RETURN NEW; END $$`, function, condition)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.pool.Exec(context.Background(), "DROP FUNCTION "+function+"() CASCADE"); err != nil {
					t.Error(err)
				}
			})
			if _, err := f.pool.Exec(t.Context(), fmt.Sprintf(`CREATE TRIGGER %s AFTER INSERT OR UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION %s()`, function, pgx.Identifier{table}.Sanitize(), function)); err != nil {
				t.Fatal(err)
			}
			sample := playbackSinkSample(2, 95)
			sample.Hints.FileID = 3
			result, err := f.store.StopPlaybackProgress(t.Context(), userstore.StopPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, StopID: "stop", FinalSample: &sample})
			if err == nil {
				t.Fatal("faulted stop committed")
			}
			if !reflect.DeepEqual(result, userstore.PlaybackProgressResult{}) {
				t.Fatalf("failed stop returned committed facts: %+v", result)
			}
			if after := f.snapshot(t); after != before {
				t.Fatalf("%s fault left partial state\nbefore=%s\nafter=%s", stage, before, after)
			}
		})
	}
}

func waitPlaybackSinkLocks(t *testing.T, f playbackSinkFixture, count int) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock'`, f.app).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= count {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("waited for %d sink locks, got %d", count, waiting)
		}
	}
}

func TestPostgresPlaybackSinkAdvanceAndStopFencing(t *testing.T) {
	f := newPlaybackSinkFixture(t)
	f.install(t)
	// Hold the profile lock so queued writes must re-read the committed fence.
	blocker, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if err := lockImportedHistory(t.Context(), blocker, f.store.userID, f.scope.ProfileID); err != nil {
		t.Fatal(err)
	}
	stale := make(chan error, 1)
	go func() {
		_, err := f.store.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, Sample: playbackSinkSample(1, 80)})
		stale <- err
	}()
	waitPlaybackSinkLocks(t, f, 1)
	state, err := loadPlaybackSink(t.Context(), blocker, f.store.userID, f.scope, true)
	if err != nil {
		t.Fatal(err)
	}
	next := f.fence
	next.Epoch++
	next.OwnerID = "new-boot"
	change, err := userstore.PreparePlaybackAuthority(state, userstore.InstallPlaybackAuthorityRequest{Scope: f.scope, Expected: &f.fence, Next: next})
	if err != nil {
		t.Fatal(err)
	}
	if err := savePlaybackSink(t.Context(), blocker, f.store.userID, change.Result.State); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-stale; !errors.Is(err, userstore.ErrPlaybackSinkStale) {
		t.Fatalf("old writer after advance: %v", err)
	}
	stop := userstore.StopPlaybackProgressRequest{Scope: f.scope, Fence: next, StopID: "stop", FinalSample: new(playbackSinkSample(2, 30))}
	results := make(chan userstore.PlaybackProgressResult, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() { r, e := f.store.StopPlaybackProgress(t.Context(), stop); results <- r; errs <- e }()
	}
	created := 0
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if (<-results).HistoryCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("concurrent stop history inserts=%d", created)
	}
	if _, err := f.store.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: f.scope, Fence: next, Sample: playbackSinkSample(3, 50)}); !errors.Is(err, userstore.ErrPlaybackSinkStopped) {
		t.Fatalf("post-stop write: %v", err)
	}
}

func lockPlaybackProjectionFixture(t *testing.T, f playbackSinkFixture) pgx.Tx {
	t.Helper()
	if _, err := f.store.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, Sample: playbackSinkSample(1, 20)}); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM user_watch_progress WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3 FOR UPDATE`, f.store.userID, f.scope.ProfileID, f.scope.MediaItemID); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestPostgresPlaybackSinkOldWriterCommitsBeforeAdvance(t *testing.T) {
	f := newPlaybackSinkFixture(t)
	f.install(t)
	blocker := lockPlaybackProjectionFixture(t, f)
	oldDone := make(chan error, 1)
	go func() {
		_, err := f.store.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, Sample: playbackSinkSample(2, 40)})
		oldDone <- err
	}()
	// The old writer owns the profile and sink locks, but cannot finish its projection.
	waitPlaybackSinkLocks(t, f, 1)
	next := f.fence
	next.Epoch++
	next.OwnerID = "successor"
	advanced := make(chan userstore.PlaybackProgressResult, 1)
	advanceErr := make(chan error, 1)
	go func() {
		r, e := f.store.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: f.scope, Expected: &f.fence, Next: next})
		advanced <- r
		advanceErr <- e
	}()
	waitPlaybackSinkLocks(t, f, 2)
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-oldDone; err != nil {
		t.Fatal(err)
	}
	if err := <-advanceErr; err != nil {
		t.Fatal(err)
	}
	result := <-advanced
	if result.State.Last == nil || result.State.Last.Sample.Sequence != 2 || result.State.Last.Sample.PositionSeconds != 40 {
		t.Fatalf("advance lost committed watermark: %+v", result.State)
	}
	if _, err := f.store.StopPlaybackProgress(t.Context(), userstore.StopPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, StopID: "stale-stop"}); !errors.Is(err, userstore.ErrPlaybackSinkStale) {
		t.Fatalf("old stop after advance: %v", err)
	}
}

func TestPostgresPlaybackSinkHistoryRemovalOrdering(t *testing.T) {
	for _, hideFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("hide-first=%v", hideFirst), func(t *testing.T) {
			f := newPlaybackSinkFixture(t)
			f.install(t)
			blocker := lockPlaybackProjectionFixture(t, f)
			stop := userstore.StopPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, StopID: "stop", FinalSample: new(playbackSinkSample(2, 95))}
			stopDone := make(chan error, 1)
			hideDone := make(chan error, 1)
			removedAt := time.Now().UTC().Add(time.Hour)
			startStop := func() { go func() { _, err := f.store.StopPlaybackProgress(t.Context(), stop); stopDone <- err }() }
			startHide := func() {
				go func() {
					hideDone <- f.store.RemoveHistoryItems(t.Context(), f.scope.ProfileID, []string{f.scope.MediaItemID}, removedAt)
				}()
			}
			if hideFirst {
				startHide()
			} else {
				startStop()
			}
			waitPlaybackSinkLocks(t, f, 1)
			if hideFirst {
				startStop()
			} else {
				startHide()
			}
			waitPlaybackSinkLocks(t, f, 2)
			if err := blocker.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := <-stopDone; err != nil {
				t.Fatal(err)
			}
			if err := <-hideDone; err != nil {
				t.Fatal(err)
			}
			history, err := f.store.ListHistory(t.Context(), f.scope.ProfileID, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			if hideFirst {
				if len(history) != 1 {
					t.Fatalf("new stop hidden after removal: %+v", history)
				}
				at, err := time.Parse(time.RFC3339Nano, history[0].WatchedAt)
				if err != nil || !at.After(removedAt) {
					t.Fatalf("stop visibility timestamp=%v err=%v", at, err)
				}
			} else if len(history) != 0 {
				t.Fatalf("removal failed to hide committed stop: %+v", history)
			}
			before := f.snapshot(t)
			result, err := f.store.StopPlaybackProgress(t.Context(), stop)
			if err != nil || result.HistoryCreated || result.Outcome != "replayed" {
				t.Fatalf("stop replay=%+v err=%v", result, err)
			}
			if after := f.snapshot(t); after != before {
				t.Fatal("stop replay changed projections or recreated removed history")
			}
		})
	}
}

func TestPostgresPlaybackSinkLostReplyReplayKeepsSyncSequence(t *testing.T) {
	f := newPlaybackSinkFixture(t)
	f.install(t)
	request := userstore.ApplyPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, Sample: playbackSinkSample(1, 40)}
	// Deliberately discard a committed reply; the retry can rely only on storage.
	if _, err := f.store.ApplyPlaybackProgress(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	readSequence := func() int64 {
		t.Helper()
		var sequence int64
		if err := f.pool.QueryRow(t.Context(), `SELECT synced_seq FROM user_watch_progress WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3`, f.store.userID, f.scope.ProfileID, f.scope.MediaItemID).Scan(&sequence); err != nil {
			t.Fatal(err)
		}
		return sequence
	}
	sequence := readSequence()
	result, err := f.store.ApplyPlaybackProgress(t.Context(), request)
	if err != nil || result.Outcome != "replayed" || result.ProgressChanged || result.HintsChanged {
		t.Fatalf("sample replay=%+v err=%v", result, err)
	}
	if after := readSequence(); after != sequence {
		t.Fatalf("sample replay advanced synced_seq %d -> %d", sequence, after)
	}
	stop := userstore.StopPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, StopID: "lost-stop"}
	if _, err := f.store.StopPlaybackProgress(t.Context(), stop); err != nil {
		t.Fatal(err)
	}
	state, err := f.store.ReadPlaybackProgress(t.Context(), f.scope)
	if err != nil || state.Stop == nil || state.Stop.History == nil {
		t.Fatalf("persisted stop=%+v err=%v", state, err)
	}
	sequence = readSequence()
	result, err = f.store.StopPlaybackProgress(t.Context(), stop)
	if err != nil || result.Outcome != "replayed" || result.HistoryCreated || result.State.Stop.History.ID != state.Stop.History.ID {
		t.Fatalf("lost stop replay=%+v err=%v", result, err)
	}
	if after := readSequence(); after != sequence {
		t.Fatalf("stop replay advanced synced_seq %d -> %d", sequence, after)
	}
}
