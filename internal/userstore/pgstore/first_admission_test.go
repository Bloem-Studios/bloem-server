package pgstore

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func admissionFixture(t *testing.T) (playbackSinkFixture, *PostgresProvider, FirstAdmissionIntent) {
	t.Helper()
	f := newPlaybackSinkFixture(t)
	installation := "ab860a0a-7da8-408d-a8de-5eb0fcd482d2"
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO server_settings(key,value) VALUES('diagnostics.server_instance_id',$1),('userdb.backend','postgres') ON CONFLICT(key) DO UPDATE SET value=excluded.value`, installation); err != nil {
		t.Fatal(err)
	}
	return f, NewPostgresProvider(f.pool), FirstAdmissionIntent{InstallationID: installation, AccountID: f.store.userID, ExpectedUsername: f.app, Backend: "postgres", SourceID: uuid.NewString(), IntentID: uuid.NewString()}
}

func TestFirstAdmissionPlanApplyAndExactReplay(t *testing.T) {
	f, p, intent := admissionFixture(t)
	if err := f.store.UpdateProgress(t.Context(), "p", "movie", 25, 100, userstore.ProgressThresholds{}); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot(t)
	decision, err := p.FirstAdmission(t.Context(), intent, false)
	if err != nil || decision.State != "eligible" {
		t.Fatalf("plan: %+v %v", decision, err)
	}
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM playback_source_markers WHERE user_id=$1)+(SELECT count(*) FROM playback_first_admissions WHERE user_id=$1)+(SELECT count(*) FROM playback_source_registrations WHERE user_id=$1)`, intent.AccountID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("plan mutated: %d %v", n, err)
	}
	first, err := p.FirstAdmission(t.Context(), intent, true)
	if err != nil || first.State != "admitted" {
		t.Fatalf("apply: %+v %v", first, err)
	}
	// Simulate an unobserved result by resolving the same retained intent, never
	// allocating another source/intent. No authority is refreshed by this read.
	replay, err := p.FirstAdmission(t.Context(), intent, true)
	if err != nil || replay.State != "already_admitted" || !replay.AdmittedAt.Equal(first.AdmittedAt) {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if after := f.snapshot(t); after != before {
		t.Fatalf("admission changed personal state: %s -> %s", before, after)
	}
	changed := intent
	changed.IntentID = uuid.NewString()
	if _, err := p.FirstAdmission(t.Context(), changed, true); err == nil {
		t.Fatal("different intent accepted")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_source_registrations SET admission_state='blocked' WHERE user_id=$1`, intent.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.FirstAdmission(t.Context(), intent, true); err == nil {
		t.Fatal("repaired changed admission")
	}
}

func TestFirstAdmissionRefusesExistingAuthorityAndWrongIdentity(t *testing.T) {
	for _, kind := range []string{"source", "registration", "sink", "installation", "account", "backend", "configured_sqlite"} {
		t.Run(kind, func(t *testing.T) {
			f, p, intent := admissionFixture(t)
			switch kind {
			case "source":
				_, err := f.pool.Exec(t.Context(), `INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES($1,$2,1,'writable')`, intent.AccountID, intent.SourceID)
				if err != nil {
					t.Fatal(err)
				}
			case "registration":
				_, err := f.pool.Exec(t.Context(), `INSERT INTO playback_source_registrations(user_id,backend,source_id,selection_generation,admission_id) VALUES($1,'postgres',$2,1,$3)`, intent.AccountID, intent.SourceID, intent.IntentID)
				if err != nil {
					t.Fatal(err)
				}
			case "sink":
				f.install(t)
			case "installation":
				intent.InstallationID = uuid.NewString()
			case "account":
				intent.ExpectedUsername = "different-account"
			case "backend":
				intent.Backend = "sqlite"
			case "configured_sqlite":
				if _, err := f.pool.Exec(t.Context(), `UPDATE server_settings SET value='sqlite' WHERE key='userdb.backend'`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.FirstAdmission(t.Context(), intent, true); err == nil {
				t.Fatal("unsafe admission accepted")
			}
			var count int
			if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM playback_first_admissions WHERE user_id=$1`, intent.AccountID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("decision created on refusal: %d %v", count, err)
			}
		})
	}
}

func awaitAdmissionLock(t *testing.T, f playbackSinkFixture, pattern string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $2)`, f.app, pattern).Scan(&waiting)
		if err != nil {
			t.Fatalf("wait for observable lock %q: %v", pattern, err)
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}

func TestFirstAdmissionOrdersLegacyWriterBeforeBarrier(t *testing.T) {
	f, p, intent := admissionFixture(t)
	if err := f.store.UpdateProgress(t.Context(), "p", "movie", 10, 100, userstore.ProgressThresholds{}); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackPlaybackSource(t.Context(), blocker)
	if _, err := blocker.Exec(t.Context(), `SELECT 1 FROM user_watch_progress WHERE user_id=$1 FOR UPDATE`, intent.AccountID); err != nil {
		t.Fatal(err)
	}
	writes := make(chan error, 1)
	go func() {
		writes <- f.store.UpdateProgress(userstore.WithLegacyPlaybackWrite(t.Context()), "p", "movie", 30, 100, userstore.ProgressThresholds{})
	}()
	awaitAdmissionLock(t, f, "%INSERT INTO user_watch_progress%")
	admissions := make(chan error, 1)
	go func() { _, err := p.FirstAdmission(t.Context(), intent, true); admissions <- err }()
	awaitAdmissionLock(t, f, "%pg_advisory_xact_lock(hashtextextended('playback-source:%")
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-writes; err != nil {
		t.Fatal(err)
	}
	if err := <-admissions; err != nil {
		t.Fatal(err)
	}
	progress, err := f.store.GetProgress(t.Context(), "p", "movie")
	if err != nil || progress.PositionSeconds != 30 {
		t.Fatalf("old write did not commit before admission: %+v %v", progress, err)
	}
	assertDelayedPlaybackRefused(t, f)
}

func TestFirstAdmissionOrdersBarrierBeforeLegacyWriter(t *testing.T) {
	f, p, intent := admissionFixture(t)
	blocker, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackPlaybackSource(t.Context(), blocker)
	if _, err := blocker.Exec(t.Context(), `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, intent.AccountID); err != nil {
		t.Fatal(err)
	}
	admissions := make(chan error, 1)
	go func() { _, err := p.FirstAdmission(t.Context(), intent, true); admissions <- err }()
	awaitAdmissionLock(t, f, "%SELECT username FROM users%")
	writes := make(chan error, 1)
	go func() {
		writes <- f.store.UpdateProgress(userstore.WithLegacyPlaybackWrite(t.Context()), "p", "movie", 30, 100, userstore.ProgressThresholds{})
	}()
	awaitAdmissionLock(t, f, "%pg_advisory_xact_lock_shared%")
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-admissions; err != nil {
		t.Fatal(err)
	}
	if err := <-writes; !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("delayed write: %v", err)
	}
	assertDelayedPlaybackRefused(t, f)
}

func assertDelayedPlaybackRefused(t *testing.T, f playbackSinkFixture) {
	t.Helper()
	ctx := userstore.WithLegacyPlaybackWrite(t.Context())
	before := f.snapshot(t)
	calls := []func() error{
		func() error {
			return f.store.UpdateProgress(ctx, "p", "movie", 50, 100, userstore.ProgressThresholds{})
		},
		func() error { return f.store.SetProgress(ctx, "p", "movie", 60, 100, userstore.ProgressThresholds{}) },
		func() error {
			_, err := f.store.SetProgressIfNewer(ctx, "p", "movie", 65, 100, false, time.Now())
			return err
		},
		func() error {
			return f.store.UpdateProgressHints(ctx, "p", "movie", userstore.VersionHints{FileID: 10})
		},
		func() error {
			return f.store.AddHistory(ctx, userstore.WatchHistoryEntry{ProfileID: "p", MediaItemID: "movie", Source: userstore.WatchHistorySourcePlayback})
		},
		func() error {
			_, err := f.store.AddVisibleHistory(ctx, userstore.WatchHistoryEntry{ProfileID: "p", MediaItemID: "movie", Source: userstore.WatchHistorySourcePlayback})
			return err
		},
	}
	for _, call := range calls {
		if err := call(); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
			t.Fatalf("playback persistence: %v", err)
		}
	}
	if after := f.snapshot(t); after != before {
		t.Fatal("delayed playback mutated personal state")
	}
	// The same setter remains available for explicit manual edits. Imported
	// progress continues through its separate monotonic import setter.
	if err := f.store.SetProgress(t.Context(), "p", "movie", 70, 100, userstore.ProgressThresholds{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetProgressIfNewer(t.Context(), "p", "imported", 20, 100, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddHistory(t.Context(), userstore.WatchHistoryEntry{ProfileID: "p", MediaItemID: "manual", Source: userstore.WatchHistorySourceManual}); err != nil {
		t.Fatal(err)
	}
}

func TestFirstAdmissionWaitsForWholeLegacyLaunchAndNestedWrites(t *testing.T) {
	f, p, intent := admissionFixture(t)
	legacyCtx, release, err := p.AcquireLegacyPlaybackAdmission(t.Context(), intent.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	admissions := make(chan error, 1)
	go func() { _, err := p.FirstAdmission(t.Context(), intent, true); admissions <- err }()
	awaitAdmissionLock(t, f, "%pg_advisory_xact_lock(hashtextextended('playback-source:%")
	// A nested transaction must join the captured launch lease, not request a
	// second shared lock behind the waiting exclusive gate (which deadlocks).
	if err := f.store.UpdateProgress(userstore.WithLegacyPlaybackWrite(legacyCtx), "p", "movie", 40, 100, userstore.ProgressThresholds{}); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-admissions; err != nil {
		t.Fatal(err)
	}
	// Queued callbacks can retain a context but cannot retain its released gate.
	if err := f.store.UpdateProgress(userstore.WithLegacyPlaybackWrite(legacyCtx), "p", "movie", 60, 100, userstore.ProgressThresholds{}); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("released lease authorized delayed callback: %v", err)
	}
	if _, done, err := p.AcquireLegacyPlaybackAdmission(t.Context(), intent.AccountID); err == nil {
		done()
		t.Fatal("legacy launch admitted after transition")
	}
}
