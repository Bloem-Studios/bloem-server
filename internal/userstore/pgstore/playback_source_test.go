package pgstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func provisionPlaybackSource(t *testing.T, f playbackSinkFixture) userstore.PlaybackSourceRef {
	t.Helper()
	ref := userstore.PlaybackSourceRef{Backend: "postgres", AccountID: f.store.userID, SourceID: uuid.NewString(), SelectionGeneration: 1}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO playback_source_markers(user_id,source_id,selection_generation,gate) VALUES($1,$2,1,'writable')`, ref.AccountID, ref.SourceID); err != nil {
		t.Fatal(err)
	}
	return ref
}
func openPlaybackSource(t *testing.T, f playbackSinkFixture, ref userstore.PlaybackSourceRef) userstore.PlaybackSinkHandle {
	t.Helper()
	h, err := NewPostgresProvider(f.pool).OpenPlaybackSink(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	return h
}
func TestPostgresPlaybackSourceExactReference(t *testing.T) {
	f := newPlaybackSinkFixture(t)
	ref := userstore.PlaybackSourceRef{Backend: "postgres", AccountID: f.store.userID, SourceID: uuid.NewString(), SelectionGeneration: 1}
	p := NewPostgresProvider(f.pool)
	if _, err := p.OpenPlaybackSink(t.Context(), ref); !errors.Is(err, userstore.ErrPlaybackSourceUnavailable) {
		t.Fatalf("missing: %v", err)
	}
	ref = provisionPlaybackSource(t, f)
	h := openPlaybackSource(t, f, ref)
	if h.Source() != ref {
		t.Fatal("source changed")
	}
	request := userstore.InstallPlaybackAuthorityRequest{Scope: f.scope, Next: f.fence}
	if _, err := f.store.InstallPlaybackAuthority(t.Context(), request); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("raw: %v", err)
	}
	if _, err := h.InstallPlaybackAuthority(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	for _, gate := range []string{"quarantined", "sealed"} {
		if _, err := f.pool.Exec(t.Context(), `UPDATE playback_source_markers SET gate=$2 WHERE user_id=$1`, ref.AccountID, gate); err != nil {
			t.Fatal(err)
		}
		if _, err := h.InstallPlaybackAuthority(t.Context(), request); !errors.Is(err, userstore.ErrPlaybackSourceUnavailable) {
			t.Fatalf("gate %s: %v", gate, err)
		}
		if _, err := p.OpenPlaybackSink(t.Context(), ref); !errors.Is(err, userstore.ErrPlaybackSourceUnavailable) {
			t.Fatalf("open gate: %v", err)
		}
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE playback_source_markers SET gate='writable',selection_generation=2 WHERE user_id=$1`, ref.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ReadPlaybackProgress(t.Context(), f.scope); !errors.Is(err, userstore.ErrPlaybackSourceMismatch) {
		t.Fatalf("old generation read: %v", err)
	}
	if _, err := h.InstallPlaybackAuthority(t.Context(), request); !errors.Is(err, userstore.ErrPlaybackSourceMismatch) {
		t.Fatalf("old generation write: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.InstallPlaybackAuthority(t.Context(), request); !errors.Is(err, userstore.ErrPlaybackSourceClosed) {
		t.Fatalf("closed: %v", err)
	}
	ref.SelectionGeneration = 2
	other := openPlaybackSource(t, f, ref)
	if _, err := other.ReadPlaybackProgress(t.Context(), f.scope); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresPlaybackSourceGateBeforeWriter(t *testing.T) {
	f := newPlaybackSinkFixture(t)
	ref := provisionPlaybackSource(t, f)
	h := openPlaybackSource(t, f, ref)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `UPDATE playback_source_markers SET gate='sealed' WHERE user_id=$1`, ref.AccountID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := h.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: f.scope, Next: f.fence})
		done <- err
	}()
	waitPlaybackSinkLocks(t, f, 1)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, userstore.ErrPlaybackSourceUnavailable) {
		t.Fatalf("writer: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM playback_progress_sinks WHERE user_id=$1`, ref.AccountID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("count %d: %v", count, err)
	}
}

func TestPostgresPlaybackSourceWriterBeforeGateAndClose(t *testing.T) {
	f := newPlaybackSinkFixture(t)
	ref := provisionPlaybackSource(t, f)
	h := openPlaybackSource(t, f, ref)
	blocker, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if err := lockImportedHistory(t.Context(), blocker, f.store.userID, f.scope.ProfileID); err != nil {
		t.Fatal(err)
	}
	writer := make(chan error, 1)
	go func() {
		_, err := h.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: f.scope, Next: f.fence})
		writer <- err
	}()
	waitPlaybackSinkLocks(t, f, 1)
	gate := make(chan error, 1)
	go func() {
		_, err := f.pool.Exec(t.Context(), `UPDATE playback_source_markers SET gate='sealed' WHERE user_id=$1`, ref.AccountID)
		gate <- err
	}()
	waitPlaybackSinkLocks(t, f, 2)
	closed := make(chan error, 1)
	go func() { closed <- h.Close() }()
	// The operation holds the handle read lock through its blocked transaction.
	concrete := h.(*postgresPlaybackSinkHandle)
	if concrete.mu.TryLock() {
		concrete.mu.Unlock()
		t.Fatal("in-flight handle was not retained")
	}
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-writer; err != nil {
		t.Fatal(err)
	}
	if err := <-gate; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, err := h.ReadPlaybackProgress(t.Context(), f.scope); !errors.Is(err, userstore.ErrPlaybackSourceClosed) {
		t.Fatalf("closed read: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM playback_progress_sinks WHERE user_id=$1`, ref.AccountID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count %d: %v", count, err)
	}
}

func TestPostgresPlaybackSourceSingleConnectionHandles(t *testing.T) {
	f := newPlaybackSinkFixture(t)
	ref := provisionPlaybackSource(t, f)
	cfg := f.pool.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	p := NewPostgresProvider(pool)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := p.OpenPlaybackSink(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := p.OpenPlaybackSink(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if _, err := first.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: f.scope, Next: f.fence}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ReadPlaybackProgress(ctx, f.scope); err != nil {
		t.Fatal(err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("handle retained pooled connection")
	}
}

func TestPostgresPlaybackSourceWrongReferenceAndRawOperations(t *testing.T) {
	f := newPlaybackSinkFixture(t)
	ref := provisionPlaybackSource(t, f)
	p := NewPostgresProvider(f.pool)
	for _, change := range []func(*userstore.PlaybackSourceRef){
		func(r *userstore.PlaybackSourceRef) { r.Backend = "sqlite" },
		func(r *userstore.PlaybackSourceRef) { r.SourceID = uuid.NewString() },
		func(r *userstore.PlaybackSourceRef) { r.SelectionGeneration++ },
	} {
		wrong := ref
		change(&wrong)
		if _, err := p.OpenPlaybackSink(t.Context(), wrong); !errors.Is(err, userstore.ErrPlaybackSourceMismatch) {
			t.Fatalf("mismatch: %v", err)
		}
	}
	raw, err := p.ForUser(t.Context(), ref.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	sink := raw.(userstore.PlaybackProgressSink)
	if _, err := sink.ReadPlaybackProgress(t.Context(), f.scope); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("raw read: %v", err)
	}
	if _, err := sink.ApplyPlaybackProgress(t.Context(), userstore.ApplyPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, Sample: playbackSinkSample(1, 20)}); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("raw apply: %v", err)
	}
	if _, err := sink.StopPlaybackProgress(t.Context(), userstore.StopPlaybackProgressRequest{Scope: f.scope, Fence: f.fence, StopID: "stop"}); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("raw stop: %v", err)
	}
	other := newPlaybackSinkFixture(t)
	wrong := ref
	wrong.AccountID = other.store.userID
	if _, err := p.OpenPlaybackSink(t.Context(), wrong); !errors.Is(err, userstore.ErrPlaybackSourceUnavailable) {
		t.Fatalf("wrong account: %v", err)
	}
}

func TestPostgresPlaybackSourceAbsentMarkerProvisionBarrier(t *testing.T) {
	f := newPlaybackSinkFixture(t)
	blocker, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if err := lockImportedHistory(t.Context(), blocker, f.store.userID, f.scope.ProfileID); err != nil {
		t.Fatal(err)
	}
	writer := make(chan error, 1)
	go func() {
		_, err := f.store.InstallPlaybackAuthority(t.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: f.scope, Next: f.fence})
		writer <- err
	}()
	waitPlaybackSinkLocks(t, f, 1)
	provisioned := make(chan error, 1)
	go func() {
		tx, err := f.pool.Begin(t.Context())
		if err != nil {
			provisioned <- err
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		_, err = tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended('playback-source:' || $1::integer::text,0))`, f.store.userID)
		if err == nil {
			_, err = tx.Exec(t.Context(), `INSERT INTO playback_source_markers(user_id,source_id,selection_generation) VALUES($1,$2,1)`, f.store.userID, uuid.NewString())
		}
		if err == nil {
			err = tx.Commit(t.Context())
		}
		provisioned <- err
	}()
	waitPlaybackSinkLocks(t, f, 2)
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-writer; err != nil {
		t.Fatal(err)
	}
	if err := <-provisioned; err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReadPlaybackProgress(t.Context(), f.scope); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("stale raw caller: %v", err)
	}
}
