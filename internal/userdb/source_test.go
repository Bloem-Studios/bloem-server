package userdb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
	"github.com/mattn/go-sqlite3"
)

func TestPlaybackSourceOpenNeverCreatesOrMigrates(t *testing.T) {
	dir := t.TempDir()
	p := NewSQLiteProvider(NewUserDBPool(PoolConfig{DataDir: dir}))
	ref := userstore.PlaybackSourceRef{Backend: "sqlite", AccountID: 1, SourceID: uuid.NewString(), SelectionGeneration: 1}
	if h, err := p.OpenPlaybackSink(t.Context(), ref); err == nil {
		_ = h.Close()
		t.Fatal("missing source opened")
	}
	if _, err := os.Stat(filepath.Join(dir, "1.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source created: %v", err)
	}
	path := filepath.Join(dir, "1.db")
	db, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE untouched(value TEXT); PRAGMA user_version=22"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if h, err := p.OpenPlaybackSink(t.Context(), ref); err == nil {
		_ = h.Close()
		t.Fatal("unmarked old source opened")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed bound open modified existing database")
	}
}

func TestPlaybackSourceHandlesCloseIndependentlyAndRejectRawStore(t *testing.T) {
	s, peer, scope, fence := sqlitePlaybackSinkFixture(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadPlaybackProgress(t.Context(), scope); err == nil {
		t.Fatal("closed handle usable")
	}
	if _, err := peer.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 30, DurationSeconds: 100}}); err != nil {
		t.Fatal(err)
	}
	raw := NewSQLiteUserStore(peer.db)
	checks := []func() error{
		func() error {
			_, err := raw.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: scope, Next: fence})
			return err
		},
		func() error {
			_, err := raw.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 2}})
			return err
		},
		func() error {
			_, err := raw.StopPlaybackProgress(t.Context(), userstore.StopPlaybackProgressRequest{Scope: scope, Fence: fence, StopID: "raw"})
			return err
		},
	}
	for _, check := range checks {
		if err := check(); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
			t.Fatalf("raw mutation=%v", err)
		}
	}
	if _, err := NewSQLiteUserStore(peer.db).ReadPlaybackProgress(t.Context(), scope); err == nil {
		t.Fatal("raw store bypassed source binding")
	}
}

func TestPlaybackSourceGateAndDurabilityCheckedInsideEachTransaction(t *testing.T) {
	s, _, scope, fence := sqlitePlaybackSinkFixture(t)
	s.db.SetMaxOpenConns(1)
	if err := s.withPlaybackSinkTransaction(t.Context(), func(exec preferenceSettingsExecutor) error {
		var synchronous int
		if err := exec.QueryRowContext(t.Context(), "PRAGMA synchronous").Scan(&synchronous); err != nil {
			return err
		}
		if synchronous != 2 {
			t.Fatalf("actual writer synchronous=%d", synchronous)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		t.Fatal(err)
	}
	request := userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 30, DurationSeconds: 100}}
	if _, err := s.ApplyPlaybackProgress(t.Context(), request); err == nil {
		t.Fatal("NORMAL writer accepted")
	}
	// Force a replacement physical connection; the exact-source DSN restores FULL.
	s.db.SetMaxIdleConns(0)
	if _, err := s.ApplyPlaybackProgress(t.Context(), request); err != nil {
		t.Fatalf("replacement writer failed: %v", err)
	}
	if _, err := s.db.Exec("UPDATE playback_source_markers SET gate='sealed'"); err != nil {
		t.Fatal(err)
	}
	request.Sample.Sequence++
	if _, err := s.ApplyPlaybackProgress(t.Context(), request); err == nil {
		t.Fatal("sealed source accepted")
	}
	var sequence int64
	if err := s.db.QueryRow("SELECT last_sequence FROM playback_progress_sinks").Scan(&sequence); err != nil || sequence != 1 {
		t.Fatalf("rejected write changed receipt: %d %v", sequence, err)
	}
}

func TestPlaybackSourceMarkerRotationWaitsForWriter(t *testing.T) {
	s, peer, scope, fence := sqlitePlaybackSinkFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		done <- s.withPlaybackSinkTransaction(ctx, func(exec preferenceSettingsExecutor) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	<-entered
	conn, err := peer.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "UPDATE playback_source_markers SET selection_generation=selection_generation+1"); err == nil {
		t.Fatal("rotation crossed held source transaction")
	} else if sqliteErr, ok := errors.AsType[sqlite3.Error](err); !ok || sqliteErr.Code != sqlite3.ErrBusy {
		t.Fatalf("rotation did not observe writer lock: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "UPDATE playback_source_markers SET selection_generation=selection_generation+1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 1}}); err == nil {
		t.Fatal("old generation survived rotation")
	}
}

func TestPlaybackSourceCloseWaitsForOwnedTransaction(t *testing.T) {
	s, peer, scope, _ := sqlitePlaybackSinkFixture(t)
	h := &sqlitePlaybackSinkHandle{SQLiteUserStore: s, ref: *s.sourceRef}
	entered, release, closing := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done, closed := make(chan error, 1), make(chan error, 1)
	go func() {
		done <- s.withPlaybackSinkTransaction(t.Context(), func(preferenceSettingsExecutor) error { close(entered); <-release; return nil })
	}()
	<-entered
	go func() { close(closing); closed <- h.Close() }()
	<-closing
	select {
	case err := <-closed:
		t.Fatalf("close crossed active transaction: %v", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ReadPlaybackProgress(t.Context(), scope); !errors.Is(err, userstore.ErrPlaybackSourceClosed) {
		t.Fatalf("closed handle: %v", err)
	}
	if _, err := peer.ReadPlaybackProgress(t.Context(), scope); err != nil {
		t.Fatalf("peer handle closed: %v", err)
	}
}

func TestPlaybackSourceRotationBeforeQueuedWrite(t *testing.T) {
	s, peer, scope, fence := sqlitePlaybackSinkFixture(t)
	conn, err := peer.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck
	if _, err := conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK") //nolint:errcheck
	if _, err := conn.ExecContext(t.Context(), "UPDATE playback_source_markers SET gate='sealed'"); err != nil {
		t.Fatal(err)
	}
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := s.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: scope, Fence: fence, Sample: userstore.PlaybackProgressSample{Sequence: 1, PositionSeconds: 30, DurationSeconds: 100}})
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("write crossed marker lock: %v", err)
	default:
	}
	if _, err := conn.ExecContext(t.Context(), "COMMIT"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, userstore.ErrPlaybackSourceUnavailable) {
		t.Fatalf("queued writer survived seal: %v", err)
	}
	var sequence int64
	if err := peer.db.QueryRow("SELECT last_sequence FROM playback_progress_sinks").Scan(&sequence); err != nil || sequence != 0 {
		t.Fatalf("sealed write effects: %d %v", sequence, err)
	}
}

func TestPlaybackSourceOpenRejectsGateIdentityAndWrongJournal(t *testing.T) {
	for _, mode := range []string{"quarantined", "sealed", "identity", "journal"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "1.db")
			db, err := NewUserDB(path, 1)
			if err != nil {
				t.Fatal(err)
			}
			ref := userstore.PlaybackSourceRef{Backend: "sqlite", AccountID: 1, SourceID: uuid.NewString(), SelectionGeneration: 1}
			gate := "writable"
			if mode == "quarantined" || mode == "sealed" {
				gate = mode
			}
			if _, err := db.DB.Exec("INSERT INTO playback_source_markers VALUES(?,?,?,?)", 1, ref.SourceID, 1, gate); err != nil {
				t.Fatal(err)
			}
			if mode == "journal" {
				if _, err := db.DB.Exec("PRAGMA journal_mode=DELETE"); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if mode == "identity" {
				ref.SourceID = uuid.NewString()
			}
			p := NewSQLiteProvider(NewUserDBPool(PoolConfig{DataDir: dir}))
			h, err := p.OpenPlaybackSink(t.Context(), ref)
			if err == nil {
				_ = h.Close()
				t.Fatalf("%s source opened", mode)
			}
			if mode == "journal" && !errors.Is(err, userstore.ErrPlaybackSourceDurability) {
				t.Fatalf("journal error=%v", err)
			}
		})
	}
}

func TestPlaybackSourceMigration24DoesNotProvisionIdentity(t *testing.T) {
	s, _, _, _ := sqlitePlaybackSinkFixture(t)
	if _, err := s.db.Exec("DROP TABLE playback_source_markers; PRAGMA user_version=23"); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(s.db); err != nil {
		t.Fatal(err)
	}
	var count, version, receipts int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM playback_source_markers").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM playback_progress_sinks").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if count != 0 || version != 24 || receipts != 1 {
		t.Fatalf("migration provisioned or erased source: markers=%d version=%d receipts=%d", count, version, receipts)
	}
}

func TestPlaybackSourceMismatchLeavesMarkedFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "1.db")
	db, err := NewUserDB(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	ref := userstore.PlaybackSourceRef{Backend: "sqlite", AccountID: 1, SourceID: uuid.NewString(), SelectionGeneration: 1}
	if _, err := db.DB.Exec("INSERT INTO playback_source_markers VALUES(?,?,1,'writable')", 1, ref.SourceID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ref.SourceID = uuid.NewString()
	p := NewSQLiteProvider(NewUserDBPool(PoolConfig{DataDir: dir}))
	if _, err := p.OpenPlaybackSink(t.Context(), ref); !errors.Is(err, userstore.ErrPlaybackSourceMismatch) {
		t.Fatalf("mismatch=%v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("mismatched source file changed")
	}
	raw, err := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close() //nolint:errcheck
	var journal string
	var version int
	if err := raw.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("journal changed: %s %v", journal, err)
	}
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 24 {
		t.Fatalf("schema changed: %d %v", version, err)
	}
}

func TestPlaybackSourceRejectsMultipleAccountMarkers(t *testing.T) {
	s, _, scope, _ := sqlitePlaybackSinkFixture(t)
	if _, err := s.db.Exec("INSERT INTO playback_source_markers VALUES(2,?,1,'writable')", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadPlaybackProgress(t.Context(), scope); !errors.Is(err, userstore.ErrPlaybackSourceUnavailable) {
		t.Fatalf("multiple marker source accepted: %v", err)
	}
}
