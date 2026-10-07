//go:build integration

package scanqueue

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/catalog"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Ordinary lower-layer transactions only; native actor/S/L authority is a later integration gate.
func nativeQueueDB(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") != "" {
		t.Fatal("SILO_TEST_DATABASE_URL must be unset")
	}
	path := "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private mode-0600 fixture required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read private fixture")
	}
	dsn, cleanup, err := bloemtestdb.CloneNativeOnboarding(t.Context(), strings.TrimSpace(string(b)), false)
	if err != nil {
		t.Fatal(err)
	}
	var p *pgxpool.Pool
	var cloneName string
	t.Cleanup(func() {
		if p != nil {
			p.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cleanup(ctx); err != nil {
			t.Error(err)
		} else {
			t.Logf("ownedUUID cleanup %s verified", cloneName)
		}
	})
	cloneCfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	templateCfg, templateErr := pgxpool.ParseConfig(strings.TrimSpace(string(b)))
	if err != nil || templateErr != nil || cloneCfg.ConnConfig.Database == templateCfg.ConnConfig.Database ||
		!strings.HasPrefix(cloneCfg.ConnConfig.Database, "bloem_storage_test_") {
		t.Fatal("clone DSN must identify a separate UUID-owned database")
	}
	cloneName = cloneCfg.ConnConfig.Database
	suffix := cloneName[strings.LastIndex(cloneName, "_")+1:]
	if len(suffix) != 32 {
		t.Fatal("clone database is not UUID-owned")
	}
	if _, err := uuid.Parse(suffix); err != nil {
		t.Fatal("clone database is not UUID-owned")
	}
	p, err = pgxpool.NewWithConfig(t.Context(), cloneCfg)
	if err != nil {
		t.Fatal("open private clone")
	}
	var actual string
	if err := p.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != cloneName || actual == templateCfg.ConnConfig.Database {
		t.Fatal("connected clone identity unverified; refusing fixture writes")
	}
	t.Logf("ownedUUID create %s caller identity verified", cloneName)
	if err := bloemtestdb.PrepareNativeOnboardingPool(t.Context(), p); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			t.Fatalf("verified clone preparation failed (SQLSTATE %s)", pe.Code)
		}
		t.Fatal("verified clone preparation failed; root protected diagnostic required")
	}
	return p, dsn
}
func queueLocalLibrary(t *testing.T, p *pgxpool.Pool) int {
	t.Helper()
	f, err := catalog.NewFolderRepository(p).Create(t.Context(), catalog.CreateFolderInput{Type: "ebook", Name: "Local queue control"})
	if err != nil {
		t.Fatal(err)
	}
	return f.ID
}
func localQueueOwner(id int) NativeLibraryAuthorizeTx {
	return func(ctx context.Context, tx pgx.Tx) error {
		var valid bool
		err := tx.QueryRow(ctx, `SELECT f.enabled AND f.type='ebook' AND o.kind IN ('platform','organization') AND NOT EXISTS(SELECT 1 FROM bloem_native_libraries n WHERE n.folder_id=f.id) FROM media_folders f JOIN resource_owners o ON o.id=f.owner_id WHERE f.id=$1 FOR SHARE OF f,o`, id).Scan(&valid)
		if err != nil {
			return err
		}
		if !valid {
			return errors.New("local ownership denied")
		}
		return nil
	}
}
func noAcceptedEvent(t *testing.T, ch <-chan evt.Envelope) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("unexpected event: %s", e.Event)
	default:
	}
}
func TestNativeLibraryQueueLowerLayerDB(t *testing.T) {
	p, _ := nativeQueueDB(t)
	hub := evt.NewHub("owned-queue-control", nil)
	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	repo := NewRepository(p)
	s := NewService(repo, nil, nil, hub, t.Context(), 1, 1)
	t.Run("nil authorizer fails before queue writes", func(t *testing.T) {
		id := queueLocalLibrary(t, p)
		run, created, err := s.EnqueueNativeLibraryAuthorized(t.Context(), id, nil)
		var e *catalog.NativeOnboardingError
		if run != nil || created || !errors.As(err, &e) || e.Code != "native_storage_unavailable" {
			t.Fatalf("nil authorizer admitted: %+v %v %v", run, created, err)
		}
		var n int
		if err := p.QueryRow(t.Context(), "SELECT count(*) FROM scan_runs WHERE media_folder_id=$1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("nil authorizer wrote queue")
		}
		noAcceptedEvent(t, ch)
	})

	t.Run("denial rolls back authorizer writes", func(t *testing.T) {
		id := queueLocalLibrary(t, p)
		deny := errors.New("revoked")
		run, created, err := s.EnqueueNativeLibraryAuthorized(t.Context(), id, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "UPDATE media_folders SET name='unauthorized' WHERE id=$1", id); err != nil {
				return err
			}
			return deny
		})
		if !errors.Is(err, deny) || run != nil || created {
			t.Fatalf("denied enqueue: %+v %v %v", run, created, err)
		}
		var name string
		if err := p.QueryRow(t.Context(), "SELECT name FROM media_folders WHERE id=$1", id).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name != "Local queue control" {
			t.Fatal("authorizer write escaped rollback")
		}
		var n int
		if err := p.QueryRow(t.Context(), "SELECT count(*) FROM scan_runs WHERE media_folder_id=$1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("denied enqueue wrote run")
		}
		noAcceptedEvent(t, ch)
	})
	t.Run("same service commits accepted then coalesces accepted running and completed", func(t *testing.T) {
		id := queueLocalLibrary(t, p)
		auth := localQueueOwner(id)
		run, created, err := s.EnqueueNativeLibraryAuthorized(t.Context(), id, auth)
		if err != nil || !created || run == nil {
			t.Fatalf("new queue run: %+v %v %v", run, created, err)
		}
		durable, err := repo.GetByID(t.Context(), run.ID)
		if err != nil || durable.MediaFolderID != id || durable.Mode != "library" || durable.Path != "" || durable.Trigger != "native_library_scan" || durable.Status != "accepted" {
			t.Fatalf("durable queue row: %+v %v", durable, err)
		}
		select {
		case e := <-ch:
			var data evt.ScanRun
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Fatal(err)
			}
			if e.Event != "scan.accepted" || data.ID != run.ID || !e.AdminOnly {
				t.Fatalf("accepted event: %+v", e)
			}
		default:
			t.Fatal("observed commit did not publish accepted")
		}
		again, created, err := s.EnqueueNativeLibraryAuthorized(t.Context(), id, auth)
		if err != nil || created || again.ID != run.ID || again.FollowupTrigger != "" {
			t.Fatalf("accepted coalesce: %+v %v %v", again, created, err)
		}
		noAcceptedEvent(t, ch)
		if _, err := repo.Start(t.Context(), run.ID); err != nil {
			t.Fatal(err)
		}
		again, created, err = s.EnqueueNativeLibraryAuthorized(t.Context(), id, auth)
		if err != nil || created || again.ID != run.ID || again.FollowupTrigger != "native_library_scan" {
			t.Fatalf("running followup: %+v %v %v", again, created, err)
		}
		noAcceptedEvent(t, ch)
		finished, followup, err := repo.CompleteWithFollowUp(t.Context(), run.ID, nil)
		if err != nil || finished.Status != "completed" || followup == nil || followup.Trigger != "native_library_scan" {
			t.Fatalf("real repository followup: %+v %+v %v", finished, followup, err)
		}
		if _, _, err := repo.MarkCancelled(t.Context(), followup.ID); err != nil {
			t.Fatal(err)
		}
		next, created, err := s.EnqueueNativeLibraryAuthorized(t.Context(), id, auth)
		if err != nil || !created || next.ID == run.ID || next.ID == followup.ID {
			t.Fatalf("completed permits new run: %+v %v %v", next, created, err)
		}
		select {
		case <-ch:
		default:
			t.Fatal("new run missed accepted event")
		}
		noAcceptedEvent(t, ch)
	})
	t.Run("two requests select one durable active run", func(t *testing.T) {
		id := queueLocalLibrary(t, p)
		start := make(chan struct{})
		var wg sync.WaitGroup
		ids := make(chan string, 2)
		createdFlags := make(chan bool, 2)
		errs := make(chan error, 2)
		for range 2 {
			wg.Go(func() {
				<-start
				run, created, err := s.EnqueueNativeLibraryAuthorized(t.Context(), id, localQueueOwner(id))
				if err == nil && run != nil {
					ids <- run.ID
				}
				createdFlags <- created
				errs <- err
			})
		}
		close(start)
		wg.Wait()
		close(ids)
		close(createdFlags)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var first string
		count := 0
		for id := range ids {
			if first != "" && first != id {
				t.Fatal("same active scope returned different IDs")
			}
			first = id
			count++
		}
		if count != 2 {
			t.Fatal("missing successful run")
		}
		n := 0
		for created := range createdFlags {
			if created {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("created %d runs", n)
		}
		select {
		case <-ch:
		default:
			t.Fatal("missing accepted event")
		}
		noAcceptedEvent(t, ch)
	})
}

func TestNativeLibraryQueueCommitOutcomesDB(t *testing.T) {
	p, _ := nativeQueueDB(t)
	hub := evt.NewHub("owned-commit-control", nil)
	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	repo := NewRepository(p)
	s := NewService(repo, nil, nil, hub, t.Context(), 1, 1)
	loss := errors.New("commit acknowledgement lost")
	t.Run("actual commit then lost acknowledgement returns known durable ID without event or replay", func(t *testing.T) {
		id := queueLocalLibrary(t, p)
		calls := 0
		q := &nativeLibraryEnqueue{service: s, commit: func(ctx context.Context, tx pgx.Tx) error {
			calls++
			if err := tx.Commit(ctx); err != nil {
				return err
			}
			return loss
		}}
		run, created, err := q.enqueue(t.Context(), id, localQueueOwner(id))
		var unknown *catalog.MutationOutcomeUnknown
		if run != nil || created || !errors.As(err, &unknown) || !errors.Is(err, loss) {
			t.Fatalf("lost acknowledgement result: %+v %v %v", run, created, err)
		}
		if unknown.OperationID == uuid.Nil || unknown.LibraryID != id || unknown.Operation != "scan" || unknown.ScanRunID == nil || *unknown.ScanRunID == "" || calls != 1 {
			t.Fatalf("missing recovery identifiers: %+v calls=%d", unknown, calls)
		}
		if unknown.CreationKey != uuid.Nil || unknown.SourceKey != nil {
			t.Fatal("ordinary control invented native recovery authority")
		}
		known, err := repo.GetByID(t.Context(), *unknown.ScanRunID)
		if err != nil || known.Status != "accepted" || known.MediaFolderID != id {
			t.Fatalf("reconciliation could not find committed row: %+v %v", known, err)
		}
		var n int
		if err := p.QueryRow(t.Context(), "SELECT count(*) FROM scan_runs WHERE media_folder_id=$1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("unknown outcome replayed enqueue: %d", n)
		}
		noAcceptedEvent(t, ch)
	})
	t.Run("coalesced acknowledgement loss returns selected ID and durable followup", func(t *testing.T) {
		id := queueLocalLibrary(t, p)
		auth := localQueueOwner(id)
		first, _, err := s.EnqueueNativeLibraryAuthorized(t.Context(), id, auth)
		if err != nil {
			t.Fatal(err)
		}
		<-ch
		if _, err := repo.Start(t.Context(), first.ID); err != nil {
			t.Fatal(err)
		}
		q := &nativeLibraryEnqueue{service: s, commit: func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.Commit(ctx); err != nil {
				return err
			}
			return loss
		}}
		_, created, err := q.enqueue(t.Context(), id, auth)
		var unknown *catalog.MutationOutcomeUnknown
		if created || !errors.As(err, &unknown) || unknown.ScanRunID == nil || *unknown.ScanRunID != first.ID {
			t.Fatalf("coalesced recovery ID: %v", err)
		}
		durable, err := repo.GetByID(t.Context(), first.ID)
		if err != nil || durable.FollowupTrigger != "native_library_scan" {
			t.Fatalf("coalesced commit missing followup: %+v %v", durable, err)
		}
		noAcceptedEvent(t, ch)
	})
	t.Run("definite rollback does not claim unknown or emit event", func(t *testing.T) {
		id := queueLocalLibrary(t, p)
		q := &nativeLibraryEnqueue{service: s, commit: func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.Rollback(ctx); err != nil {
				return err
			}
			return pgx.ErrTxCommitRollback
		}}
		run, created, err := q.enqueue(t.Context(), id, localQueueOwner(id))
		var unknown *catalog.MutationOutcomeUnknown
		if run != nil || created || err == nil || errors.As(err, &unknown) || !errors.Is(err, pgx.ErrTxCommitRollback) {
			t.Fatalf("definite rollback: %+v %v %v", run, created, err)
		}
		var n int
		if err := p.QueryRow(t.Context(), "SELECT count(*) FROM scan_runs WHERE media_folder_id=$1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("known rollback committed row")
		}
		noAcceptedEvent(t, ch)
	})
	t.Run("ambiguous commit refusal remains unknown and retains attempted ID", func(t *testing.T) {
		id := queueLocalLibrary(t, p)
		q := &nativeLibraryEnqueue{service: s, commit: func(context.Context, pgx.Tx) error { return loss }}
		_, created, err := q.enqueue(t.Context(), id, localQueueOwner(id))
		var unknown *catalog.MutationOutcomeUnknown
		if created || !errors.As(err, &unknown) || unknown.ScanRunID == nil || *unknown.ScanRunID == "" {
			t.Fatalf("ambiguous commit treated definite: %v", err)
		}
		if _, err := repo.GetByID(t.Context(), *unknown.ScanRunID); !errors.Is(err, ErrScanRunNotFound) {
			t.Fatalf("uncommitted attempt escaped rollback: %v", err)
		}
		noAcceptedEvent(t, ch)
	})
}

func TestNativeLibraryQueueBoundedNoRowRetryDB(t *testing.T) {
	p, _ := nativeQueueDB(t)
	hub := evt.NewHub("owned-retry-control", nil)
	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	s := NewService(NewRepository(p), nil, nil, hub, t.Context(), 1, 1)
	// A finite SQL fault suppresses INSERT's RETURNING row. Coalescing runs against
	// the real scan_runs table and returns no active row. This is a lower-layer
	// no-row fault control, not evidence for a native concurrent lifecycle race.
	if _, err := p.Exec(t.Context(), `CREATE FUNCTION owned_queue_no_row() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF current_setting('bloem.owned_queue_no_row',true)='on' THEN RETURN NULL; END IF; RETURN NEW; END $$;
 CREATE TRIGGER owned_queue_no_row BEFORE INSERT ON scan_runs FOR EACH ROW EXECUTE FUNCTION owned_queue_no_row()`); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"fresh retry succeeds", "fresh retry revoked", "two missing rows refuse"} {
		t.Run(mode, func(t *testing.T) {
			id := queueLocalLibrary(t, p)
			auth := localQueueOwner(id)
			calls := 0
			var first pgx.Tx
			denied := errors.New("retry authority revoked")
			run, created, err := s.EnqueueNativeLibraryAuthorized(t.Context(), id, func(ctx context.Context, tx pgx.Tx) error {
				calls++
				if err := auth(ctx, tx); err != nil {
					return err
				}
				if calls == 1 {
					first = tx
				} else if tx == first {
					t.Error("queue retry reused transaction")
				}
				if calls == 2 && mode == "fresh retry revoked" {
					return denied
				}
				if calls == 1 || mode == "two missing rows refuse" {
					if _, err := tx.Exec(ctx, "SET LOCAL bloem.owned_queue_no_row='on'"); err != nil {
						return err
					}
					_, err := tx.Exec(ctx, "UPDATE media_folders SET name='rolled back no-row attempt' WHERE id=$1", id)
					return err
				}
				return nil
			})
			if calls != 2 {
				t.Fatalf("bounded retry authorization calls=%d err=%v", calls, err)
			}
			if mode == "fresh retry succeeds" {
				if err != nil || !created || run == nil {
					t.Fatalf("fresh retry: %+v %v %v", run, created, err)
				}
				select {
				case <-ch:
				default:
					t.Fatal("fresh retry missed accepted")
				}
				noAcceptedEvent(t, ch)
			} else {
				if run != nil || created || err == nil {
					t.Fatalf("no-row/revocation falsely succeeded: %+v %v %v", run, created, err)
				}
				if mode == "fresh retry revoked" && !errors.Is(err, denied) {
					t.Fatalf("fresh denial lost: %v", err)
				}
				if mode == "two missing rows refuse" {
					var unavailable *catalog.NativeOnboardingError
					if !errors.As(err, &unavailable) || unavailable.Code != "native_storage_unavailable" {
						t.Fatalf("exhausted retry: %v", err)
					}
				}
				noAcceptedEvent(t, ch)
			}
			var name string
			if err := p.QueryRow(t.Context(), "SELECT name FROM media_folders WHERE id=$1", id).Scan(&name); err != nil {
				t.Fatal(err)
			}
			if name != "Local queue control" {
				t.Fatal("no-row attempt committed authorizer writes")
			}
		})
	}
}

func TestNativeLibraryQueueOriginalRepositoryDB(t *testing.T) {
	_, writerDSN := nativeQueueDB(t)
	// Original tests open their own pools from this exact verified clone URL.
	t.Setenv("SILO_TEST_DATABASE_URL", writerDSN)
	for _, tt := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"claim skips direct", TestClaimNextAcceptedSkipsDirectAdminRuns},
		{"stale janitor", TestStaleJanitorFailsDirectRunsAndRequeuesQueuedRuns},
		{"heartbeat", TestStaleJanitorLeavesHeartbeatingDirectRunRunning},
		{"running followup", TestCreateOnRunningScopeOwesFollowUpEnqueuedOnComplete},
		{"direct trigger followup", TestCreateOnRunningScopeIgnoresDirectAdminTriggers},
	} {
		t.Run(tt.name, tt.run)
	}
}

func TestNativeLibraryQueueTerminalCoalesceRetryDB(t *testing.T) {
	p, _ := nativeQueueDB(t)
	hub := evt.NewHub("owned-terminal-control", nil)
	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	repo := NewRepository(p)
	s := NewService(repo, nil, nil, hub, t.Context(), 1, 1)
	// The real coalescer can select an active ID and receive a terminal row if
	// its updater wins after a completion. This finite SQL fault models the
	// returned-row boundary, not an authenticated native lifecycle race.
	if _, err := p.Exec(t.Context(), `CREATE FUNCTION owned_queue_terminal() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF current_setting('bloem.owned_queue_terminal',true)='on' THEN NEW.status='completed'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER owned_queue_terminal BEFORE UPDATE ON scan_runs FOR EACH ROW EXECUTE FUNCTION owned_queue_terminal()`); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"fresh retry selects active", "fresh retry denies", "terminal rows exhaust bound"} {
		t.Run(mode, func(t *testing.T) {
			id := queueLocalLibrary(t, p)
			auth := localQueueOwner(id)
			first, created, err := repo.Create(t.Context(), CreateInput{LibraryID: id, Mode: ModeLibrary, Path: "", Trigger: "manual"})
			if err != nil || !created {
				t.Fatalf("local active control: %+v %v %v", first, created, err)
			}
			calls := 0
			denied := errors.New("fresh authority denied")
			run, created, err := s.EnqueueNativeLibraryAuthorized(t.Context(), id, func(ctx context.Context, tx pgx.Tx) error {
				calls++
				if err := auth(ctx, tx); err != nil {
					return err
				}
				if calls == 2 && mode == "fresh retry denies" {
					return denied
				}
				if calls == 1 || mode == "terminal rows exhaust bound" {
					_, err := tx.Exec(ctx, "SET LOCAL bloem.owned_queue_terminal='on'")
					return err
				}
				return nil
			})
			if calls != 2 {
				t.Fatalf("terminal coalesce skipped fresh authority: calls=%d run=%+v created=%v err=%v", calls, run, created, err)
			}
			if mode == "fresh retry selects active" {
				if err != nil || created || run == nil || run.ID != first.ID || run.Status != "accepted" {
					t.Fatalf("fresh active result: %+v %v %v", run, created, err)
				}
			} else {
				if err == nil || run != nil || created {
					t.Fatalf("terminal coalesce falsely succeeded: %+v %v %v", run, created, err)
				}
				if mode == "fresh retry denies" && !errors.Is(err, denied) {
					t.Fatalf("fresh denial lost: %v", err)
				}
			}
			durable, err := repo.GetByID(t.Context(), first.ID)
			if err != nil || durable.Status != "accepted" {
				t.Fatalf("terminal fault committed: %+v %v", durable, err)
			}
			noAcceptedEvent(t, ch)
		})
	}
}

type nativeCommitObservationBus struct {
	observe func(context.Context, cache.Event) error
}

func (b nativeCommitObservationBus) Publish(ctx context.Context, _ string, event cache.Event) error {
	return b.observe(ctx, event)
}
func (nativeCommitObservationBus) Subscribe(context.Context, string, cache.EventHandler) error {
	return nil
}
func (nativeCommitObservationBus) Close() error { return nil }

func TestNativeLibraryQueueEventCommitOrderDB(t *testing.T) {
	p, _ := nativeQueueDB(t)
	id := queueLocalLibrary(t, p)
	observed := 0
	bus := nativeCommitObservationBus{observe: func(ctx context.Context, event cache.Event) error {
		var envelope evt.Envelope
		if err := json.Unmarshal([]byte(event.Payload), &envelope); err != nil {
			t.Fatal(err)
		}
		var run evt.ScanRun
		if err := json.Unmarshal(envelope.Data, &run); err != nil {
			t.Fatal(err)
		}
		// Publish is synchronous. An independent connection must see the row at
		// publication time, so moving the event before COMMIT fails this assertion.
		var status string
		if err := p.QueryRow(ctx, "SELECT status FROM scan_runs WHERE id=$1", run.ID).Scan(&status); err != nil || status != "accepted" {
			t.Errorf("published before observed commit: status=%q err=%v", status, err)
		}
		observed++
		return nil
	}}
	s := NewService(NewRepository(p), nil, nil, evt.NewHub("owned-commit-order", bus), t.Context(), 1, 1)
	if _, created, err := s.EnqueueNativeLibraryAuthorized(t.Context(), id, localQueueOwner(id)); err != nil || !created {
		t.Fatalf("enqueue: %v %v", created, err)
	}
	if observed != 1 {
		t.Fatalf("event observations=%d", observed)
	}
}
