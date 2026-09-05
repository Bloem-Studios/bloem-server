package userdb

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/storetest"
)

func TestSQLitePlaybackSinkRawConformance(t *testing.T) {
	storetest.PlaybackSink(t, newConformanceStore)
}

func TestSQLitePlaybackSinkConformance(t *testing.T) {
	storetest.PlaybackSink(t, func(t *testing.T) userstore.UserStore { s, _, _, _ := sqlitePlaybackSinkFixture(t); return s })
}

func sqlitePlaybackSinkFixture(t *testing.T) (*SQLiteUserStore, *SQLiteUserStore, userstore.PlaybackProgressScope, userstore.PlaybackProgressFence) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "1.db")
	first, err := NewUserDB(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := NewUserDB(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	ref := userstore.PlaybackSourceRef{Backend: "sqlite", AccountID: 1, SourceID: uuid.NewString(), SelectionGeneration: 1}
	if _, err := first.DB.Exec(`INSERT INTO playback_source_markers VALUES(?,?,?,'writable')`, ref.AccountID, ref.SourceID, ref.SelectionGeneration); err != nil {
		t.Fatal(err)
	}
	provider := NewSQLiteProvider(NewUserDBPool(PoolConfig{DataDir: filepath.Dir(path)}))
	open := func() *SQLiteUserStore {
		h, err := provider.OpenPlaybackSink(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close() })
		return h.(*sqlitePlaybackSinkHandle).SQLiteUserStore
	}
	bound, peer := open(), open()
	scope := userstore.PlaybackProgressScope{ProfileID: "profile", SessionID: uuid.NewString(), MediaItemID: "movie"}
	fence := userstore.PlaybackProgressFence{AttemptID: uuid.NewString(), Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1}
	s := bound
	if _, err := s.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: scope, Next: fence}); err != nil {
		t.Fatal(err)
	}
	return s, peer, scope, fence
}

func TestSQLitePlaybackSinkConcurrentStop(t *testing.T) {
	first, second, scope, fence := sqlitePlaybackSinkFixture(t)
	request := userstore.StopPlaybackProgressRequest{Scope: scope, Fence: fence, StopID: "stop", FinalSample: &userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 95, DurationSeconds: 100}}
	type outcome struct {
		result userstore.PlaybackProgressResult
		err    error
	}
	results := make(chan outcome, 16)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 16 {
		store := first
		if i%2 != 0 {
			store = second
		}
		wg.Go(func() {
			<-gate
			result, err := store.StopPlaybackProgress(t.Context(), request)
			results <- outcome{result, err}
		})
	}
	close(gate)
	wg.Wait()
	close(results)
	created := 0
	historyID := ""
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.result.HistoryCreated {
			created++
		}
		if got.result.State.Stop == nil || got.result.State.Stop.History == nil {
			t.Fatal("missing terminal receipt")
		}
		if historyID == "" {
			historyID = got.result.State.Stop.History.ID
		}
		if got.result.State.Stop.History.ID != historyID {
			t.Fatal("concurrent replay returned another history identity")
		}
	}
	var count int
	if err := first.db.QueryRow(`SELECT COUNT(*) FROM watch_history`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if created != 1 || count != 1 {
		t.Fatalf("stop effects created=%d rows=%d", created, count)
	}
}

func TestSQLitePlaybackSinkReceiptFailureRollsBackEveryProjection(t *testing.T) {
	s, _, scope, fence := sqlitePlaybackSinkFixture(t)
	sample := userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 30, DurationSeconds: 100, Hints: userstore.VersionHints{FileID: 1, Resolution: "720p"}}
	if _, err := s.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: sample}); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetProgress(t.Context(), scope.ProfileID, scope.MediaItemID)
	if err != nil {
		t.Fatal(err)
	}
	var sequence int64
	if err := s.db.QueryRow(`SELECT synced_seq FROM watch_progress`).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_playback_receipt BEFORE UPDATE ON playback_progress_sinks BEGIN SELECT RAISE(ABORT, 'injected receipt failure'); END;`); err != nil {
		t.Fatal(err)
	}
	sample.Sequence, sample.PositionSeconds, sample.Hints.FileID = 2, 95, 2
	result, err := s.StopPlaybackProgress(t.Context(), userstore.StopPlaybackProgressRequest{Scope: scope, Fence: fence, StopID: "stop", FinalSample: &sample})
	if err == nil || !reflect.DeepEqual(result, userstore.PlaybackProgressResult{}) {
		t.Fatalf("failed mutation returned committed result: %+v %v", result, err)
	}
	after, err := s.GetProgress(t.Context(), scope.ProfileID, scope.MediaItemID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("projection survived receipt rollback: before=%+v after=%+v", before, after)
	}
	var afterSequence int64
	var history int
	if err := s.db.QueryRow(`SELECT synced_seq FROM watch_progress`).Scan(&afterSequence); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM watch_history`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	state, err := s.ReadPlaybackProgress(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if afterSequence != sequence || history != 0 || state.Stop != nil || state.Last.Sample.Sequence != 1 {
		t.Fatal("receipt rollback left partial state")
	}
}

func TestSQLitePlaybackSinkCancellationReleasesWriter(t *testing.T) {
	s, peer, scope, fence := sqlitePlaybackSinkFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	err := s.withPlaybackSinkTransaction(ctx, func(exec preferenceSettingsExecutor) error {
		if _, err := exec.ExecContext(ctx, `UPDATE playback_progress_sinks SET owner_id='uncommitted'`); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled writer=%v", err)
	}
	ctx, done := context.WithTimeout(t.Context(), time.Second)
	defer done()
	result, err := peer.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 30, DurationSeconds: 100}})
	if err != nil || !result.ProgressChanged {
		t.Fatalf("writer reservation leaked after cancellation: %+v %v", result, err)
	}
}

func TestSQLitePlaybackSinkAdvanceWaitsForSelectedWriter(t *testing.T) {
	s, peer, scope, fence := sqlitePlaybackSinkFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	written, release := make(chan struct{}), make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- s.withPlaybackSinkTransaction(ctx, func(exec preferenceSettingsExecutor) error {
			state, err := readPlaybackSink(ctx, exec, scope)
			if err != nil {
				return err
			}
			sample := userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 30, DurationSeconds: 100}
			change, err := userstore.PreparePlaybackProgress(state, userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: sample})
			if err != nil {
				return err
			}
			if err := setPlaybackProgress(exec, scope.ProfileID, scope.MediaItemID, 30, 100, sample.Thresholds); err != nil {
				return err
			}
			if err := savePlaybackSink(ctx, exec, change.Result.State); err != nil {
				return err
			}
			close(written)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-written:
	case <-ctx.Done():
		t.Fatal("selected writer never reached transaction barrier")
	}
	next := fence
	next.Epoch++
	next.OwnerID = uuid.NewString()
	type installResult struct {
		result userstore.PlaybackProgressResult
		err    error
	}
	installerStarted := make(chan struct{})
	installerDone := make(chan installResult, 1)
	go func() {
		close(installerStarted)
		result, err := peer.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: scope, Expected: &fence, Next: next})
		installerDone <- installResult{result, err}
	}()
	<-installerStarted
	select {
	case got := <-installerDone:
		t.Fatalf("install crossed held SQLite writer transaction: %+v", got)
	default:
	}
	close(release)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	got := <-installerDone
	if got.err != nil || got.result.State.Last == nil || got.result.State.Last.Sample.Sequence != 1 || got.result.State.Last.Fence != fence {
		t.Fatalf("advance lost committed predecessor sample: %+v %v", got.result, got.err)
	}
	if _, err := s.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 2, PositionSeconds: 90, DurationSeconds: 100}}); !errors.Is(err, userstore.ErrPlaybackSinkStale) {
		t.Fatalf("old selected writer survived advance: %v", err)
	}
}

func TestSQLitePlaybackSinkUsesOneConnection(t *testing.T) {
	s, _, scope, fence := sqlitePlaybackSinkFixture(t)
	s.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result, err := s.StopPlaybackProgress(ctx, userstore.StopPlaybackProgressRequest{Scope: scope, Fence: fence, StopID: "single", FinalSample: &userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 95, DurationSeconds: 100, Hints: userstore.VersionHints{FileID: 4}}})
	if err != nil || !result.ProgressChanged || !result.HintsChanged || !result.HistoryCreated {
		t.Fatalf("transaction escaped pinned connection: %+v %v", result, err)
	}
}

func TestSQLitePlaybackSinkMigration23And24PreservesExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := NewUserDB(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateProgress(db.DB, "profile", "movie", 30, 100, userstore.ProgressThresholds{}); err != nil {
		t.Fatal(err)
	}
	if err := AddHistory(db.DB, userstore.WatchHistoryEntry{ID: "existing", ProfileID: "profile", MediaItemID: "movie"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`DROP TABLE playback_progress_sinks; DROP TABLE playback_source_markers; PRAGMA user_version=22;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Exercise the migration itself before InitSchema could recreate its table.
	raw, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_foreign_keys=ON")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if err := runMigrations(raw); err != nil {
		t.Fatal(err)
	}
	version, err := userVersion(raw)
	if err != nil || version != schemaVersion {
		t.Fatalf("upgrade version=%d %v", version, err)
	}
	ref := userstore.PlaybackSourceRef{Backend: "sqlite", AccountID: 1, SourceID: uuid.NewString(), SelectionGeneration: 1}
	if _, err := raw.Exec(`INSERT INTO playback_source_markers VALUES(?,?,?,'writable')`, ref.AccountID, ref.SourceID, ref.SelectionGeneration); err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec("PRAGMA synchronous=FULL"); err != nil {
		t.Fatal(err)
	}
	s := &SQLiteUserStore{db: raw, sourceRef: &ref}
	progress, err := s.GetProgress(t.Context(), "profile", "movie")
	if err != nil || progress == nil || progress.PositionSeconds != 30 {
		t.Fatalf("upgrade changed progress: %+v %v", progress, err)
	}
	var count int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM watch_history WHERE id='existing'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("upgrade changed history: %d %v", count, err)
	}
	scope := userstore.PlaybackProgressScope{ProfileID: "profile", SessionID: "session", MediaItemID: "movie"}
	fence := userstore.PlaybackProgressFence{AttemptID: "attempt", Incarnation: "incarnation", OwnerID: "owner", Epoch: 1}
	if _, err := s.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: scope, Next: fence}); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewUserDB(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	reopened.DB.SetMaxOpenConns(1)
	if _, err := reopened.DB.Exec("PRAGMA synchronous=FULL"); err != nil {
		t.Fatal(err)
	}
	state, err := (&SQLiteUserStore{db: reopened.DB, sourceRef: &ref}).ReadPlaybackProgress(t.Context(), scope)
	if err != nil || state.Fence != fence {
		t.Fatalf("reopen lost installed authority: %+v %v", state, err)
	}
}

func TestSQLitePlaybackSinkAdvanceCommitsBeforeQueuedOldWriter(t *testing.T) {
	s, peer, scope, fence := sqlitePlaybackSinkFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	next := fence
	next.Epoch++
	next.OwnerID = uuid.NewString()
	written, release := make(chan struct{}), make(chan struct{})
	advanceDone := make(chan error, 1)
	go func() {
		advanceDone <- s.withPlaybackSinkTransaction(ctx, func(exec preferenceSettingsExecutor) error {
			state, err := readPlaybackSink(ctx, exec, scope)
			if err != nil {
				return err
			}
			change, err := userstore.PreparePlaybackAuthority(state, userstore.InstallPlaybackAuthorityRequest{Scope: scope, Expected: &fence, Next: next})
			if err != nil {
				return err
			}
			if err := savePlaybackSink(ctx, exec, change.Result.State); err != nil {
				return err
			}
			close(written)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-written:
	case <-ctx.Done():
		t.Fatal("advance did not reach transaction barrier")
	}
	started, oldDone := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := peer.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 30, DurationSeconds: 100}})
		oldDone <- err
	}()
	<-started
	select {
	case err := <-oldDone:
		t.Fatalf("old writer crossed held advance: %v", err)
	default:
	}
	close(release)
	if err := <-advanceDone; err != nil {
		t.Fatal(err)
	}
	if err := <-oldDone; !errors.Is(err, userstore.ErrPlaybackSinkStale) {
		t.Fatalf("queued old writer=%v", err)
	}
	state, err := s.ReadPlaybackProgress(ctx, scope)
	if err != nil || state.Fence != next || state.Last != nil {
		t.Fatalf("stale writer changed receipt: %+v %v", state, err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM watch_progress`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale writer projected progress: %d %v", count, err)
	}
}

func TestSQLitePlaybackSinkLostCommitReplyReplaysWithoutProjection(t *testing.T) {
	s, peer, scope, fence := sqlitePlaybackSinkFixture(t)
	request := userstore.StopPlaybackProgressRequest{Scope: scope, Fence: fence, StopID: "lost-reply", FinalSample: &userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 95, DurationSeconds: 100, Hints: userstore.VersionHints{FileID: 7}}}
	// The transport loses the successful result; retry must recover the stored receipt.
	if _, err := s.StopPlaybackProgress(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	var historyID string
	var sequence int64
	if err := s.db.QueryRow(`SELECT id FROM watch_history`).Scan(&historyID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT synced_seq FROM watch_progress`).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	replay, err := peer.StopPlaybackProgress(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replay.State.Stop == nil || replay.State.Stop.History == nil || replay.State.Stop.History.ID != historyID || replay.ProgressChanged || replay.HintsChanged || replay.HistoryCreated {
		t.Fatalf("lost reply repeated effects: %+v", replay)
	}
	var afterSequence int64
	if err := s.db.QueryRow(`SELECT synced_seq FROM watch_progress`).Scan(&afterSequence); err != nil {
		t.Fatal(err)
	}
	if afterSequence != sequence {
		t.Fatalf("replay changed sync sequence: %d -> %d", sequence, afterSequence)
	}
}
