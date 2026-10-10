package adminjob

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type bloemFencedDeleteExecutor struct {
	executor *LibraryDeleteExecutor
	fenced   func(context.Context)
}

func (e bloemFencedDeleteExecutor) Execute(ctx context.Context, req DeleteLibraryRequest, progress func(int, int, string)) (*DeleteLibraryResult, error) {
	started := false
	return e.executor.Execute(ctx, req, func(current, total int, message string) {
		if !started && message == "Deleting orphaned items" {
			started = true
			e.fenced(ctx)
			// A large library can produce many batch callbacks while one writer
			// occupies the pool. Their individual deadlines must not accumulate.
			for range 100 {
				progress(current, total, message)
			}
		}
		progress(current, total, message)
	})
}

type bloemCanceledDeleteExecutor struct{ executor *LibraryDeleteExecutor }

func (e bloemCanceledDeleteExecutor) Execute(ctx context.Context, req DeleteLibraryRequest, progress func(int, int, string)) (*DeleteLibraryResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return e.executor.Execute(ctx, req, func(current, total int, message string) {
		if message == "Deleting orphaned items" {
			cancel()
		}
		progress(current, total, message)
	})
}

func TestBloemLibraryDeletionRunnerCompletesWithFencedSharedPool(t *testing.T) {
	control := lifecycleRepo(t)
	cfg := control.pool.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.MinIdleConns = 0
	application := "bloem-deletion-metadata-" + uuid.NewString()
	cfg.ConnConfig.RuntimeParams["application_name"] = application
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := NewRepository(pool)
	var folder int
	if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name) VALUES('movies','Runner contention') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	job, err := repo.CreateLibraryDeletion(t.Context(), bloemLifecycleActor(t, control), DeleteLibraryRequest{LibraryID: folder})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimNextQueued(t.Context(), JobTypeDeleteLibrary)
	if err != nil || claimed == nil {
		t.Fatalf("claim deletion: %v", err)
	}
	contenderDone := make(chan error, 1)
	contenderPID := make(chan uint32, 1)
	contenderCtx, cancelContender := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelContender()
	executor := bloemFencedDeleteExecutor{
		executor: NewLibraryDeleteExecutor(catalog.NewFolderRepository(pool), nil, nil),
		fenced: func(ctx context.Context) {
			// The real executor's initial progress must be observable before
			// the bundle writer consumes the shared pool's only connection.
			observed, err := control.GetByID(ctx, job.ID)
			if err != nil || observed.Status != StatusRunning || observed.Message != "Counting library contents" {
				t.Fatalf("initial progress: job=%+v error=%v", observed, err)
			}
			go func() {
				conn, err := pool.Acquire(contenderCtx)
				if err != nil {
					contenderDone <- err
					return
				}
				defer conn.Release()
				contenderPID <- conn.Conn().PgConn().PID()
				_, err = conn.Exec(contenderCtx, `INSERT INTO entitlement_bundle_members(bundle_id,bundle_revision,entitlement_kind,root_kind,root_owner_id,media_folder_id)
				 SELECT b.id,b.active_revision,'library_access','media_folder',f.owner_id,f.id
				 FROM entitlement_bundles b CROSS JOIN media_folders f WHERE b.is_organization_creation_default AND f.id=$1`, folder)
				contenderDone <- err
			}()
			var pid uint32
			select {
			case pid = <-contenderPID:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var blocked bool
				if err := control.pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-contenderDone:
					t.Fatalf("bundle writer did not reach fence: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-ticker.C:
				}
			}
			// Model a stale-check window while the ordinary pool is exhausted.
			// The real heartbeat must replace this stale timestamp before a
			// second runner could recover and invalidate the deletion's claim.
			if _, err := control.pool.Exec(ctx, `UPDATE admin_jobs SET heartbeat_at=NOW()-INTERVAL '10 minutes' WHERE id=$1`, job.ID); err != nil {
				t.Fatal(err)
			}
			cutoff := time.Now().UTC().Add(-time.Minute)
			heartbeatCtx, cancelHeartbeat := context.WithTimeout(ctx, 2*time.Second)
			defer cancelHeartbeat()
			for {
				var live bool
				if err := control.pool.QueryRow(heartbeatCtx, `SELECT heartbeat_at>$2 FROM admin_jobs WHERE id=$1`, job.ID, cutoff).Scan(&live); err != nil {
					t.Fatal(err)
				}
				if live {
					break
				}
				select {
				case <-heartbeatCtx.Done():
					t.Fatal("deletion heartbeat stalled behind the fenced shared pool")
				case <-ticker.C:
				}
			}
			if recovered, err := control.RequeueStaleRunning(ctx, cutoff); err != nil || recovered != 0 {
				t.Fatalf("live deletion lost its claim: requeued=%d error=%v", recovered, err)
			}
		},
	}
	runner := NewRunner(repo.withClaim(claimed), nil, nil, nil, nil, executor, nil, nil, nil)
	runner.heartbeatInterval = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	runner.workCtx = ctx
	runner.executeDeleteLibrary(claimed)
	terminal, err := control.GetByID(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Status != StatusCompleted || terminal.ProgressCurrent != 5 || terminal.ProgressTotal != 5 || terminal.ClaimGeneration != claimed.ClaimGeneration {
		t.Fatalf("deletion failed to complete under pool contention: status=%s progress=%d/%d claim=%d error=%s", terminal.Status, terminal.ProgressCurrent, terminal.ProgressTotal, terminal.ClaimGeneration, terminal.ErrorMessage)
	}
	if attempts := pool.Stat().CanceledAcquireCount(); attempts != 0 {
		t.Fatalf("deletion metadata attempted %d acquisitions in the exhausted caller pool", attempts)
	}
	select {
	case err := <-contenderDone:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("fenced writer outcome = %v, want FK rejection", err)
		}
	case <-contenderCtx.Done():
		t.Fatal("bundle writer stayed blocked after deletion")
	}
	var roots, grants int
	if err := control.pool.QueryRow(t.Context(), `SELECT
	 (SELECT count(*) FROM media_folders WHERE id=$1),
	 (SELECT count(*) FROM organization_entitlements WHERE media_folder_id=$1)`, folder).Scan(&roots, &grants); err != nil {
		t.Fatal(err)
	}
	if roots != 0 || grants != 0 {
		t.Fatalf("completed deletion retained root/grants: %d/%d", roots, grants)
	}
	bloemWaitForRunnerConnections(t, control.pool, application, 1)
}

func TestBloemLibraryDeletionRunnerMetadataFailurePreservesCatalogAndClosesConnections(t *testing.T) {
	for _, stage := range []string{"before-connect", "after-connect", "cancel-after-admission"} {
		t.Run(stage, func(t *testing.T) {
			control := lifecycleRepo(t)
			cfg := control.pool.Config()
			cfg.MaxConns, cfg.MinConns, cfg.MinIdleConns = 1, 0, 0
			application := "bloem-delete-metadata-fail-" + uuid.NewString()
			cfg.ConnConfig.RuntimeParams["application_name"] = application
			forced := errors.New("forced metadata connection failure")
			var before, after atomic.Int32
			cfg.BeforeConnect = func(context.Context, *pgx.ConnConfig) error {
				if before.Add(1) == 2 && stage == "before-connect" {
					return forced
				}
				return nil
			}
			cfg.AfterConnect = func(context.Context, *pgx.Conn) error {
				if after.Add(1) == 2 && stage == "after-connect" {
					return forced
				}
				return nil
			}
			pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			var folder int
			if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name) VALUES('movies','Metadata initialization failure') RETURNING id`).Scan(&folder); err != nil {
				t.Fatal(err)
			}
			content := "metadata-initialization-" + uuid.NewString()
			for _, query := range []string{
				`INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Retained metadata fixture')`,
				`INSERT INTO media_files(content_id,media_folder_id,file_path) VALUES($1,$2,'/synthetic/metadata-failure.mkv')`,
				`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`,
			} {
				args := []any{content, folder}
				if !strings.Contains(query, "$2") {
					args = args[:1]
				}
				if _, err := pool.Exec(t.Context(), query, args...); err != nil {
					t.Fatal(err)
				}
			}
			repo := NewRepository(pool)
			job, err := repo.CreateLibraryDeletion(t.Context(), bloemLifecycleActor(t, control), DeleteLibraryRequest{LibraryID: folder})
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := repo.ClaimNextQueued(t.Context(), JobTypeDeleteLibrary)
			if err != nil || claimed == nil {
				t.Fatalf("claim deletion: %v", err)
			}
			realExecutor := NewLibraryDeleteExecutor(catalog.NewFolderRepository(pool), nil, nil)
			var executor deleteLibraryExecutor = realExecutor
			if stage == "cancel-after-admission" {
				executor = bloemCanceledDeleteExecutor{executor: realExecutor}
			}
			runner := NewRunner(repo.withClaim(claimed), nil, nil, nil, nil, executor, nil, nil, nil)
			runner.executeDeleteLibrary(claimed)
			terminal, err := control.GetByID(t.Context(), job.ID)
			if err != nil || terminal.Status != StatusFailed || terminal.ClaimGeneration != claimed.ClaimGeneration {
				t.Fatalf("failed initialization outcome: job=%+v error=%v", terminal, err)
			}
			var roots, items, files, links, grants int
			if err := control.pool.QueryRow(t.Context(), `SELECT
			 (SELECT count(*) FROM media_folders WHERE id=$1),
			 (SELECT count(*) FROM media_items WHERE content_id=$2),
			 (SELECT count(*) FROM media_files WHERE media_folder_id=$1),
			 (SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1),
			 (SELECT count(*) FROM organization_entitlements WHERE media_folder_id=$1)`, folder, content).Scan(&roots, &items, &files, &links, &grants); err != nil {
				t.Fatal(err)
			}
			if roots != 1 || items != 1 || files != 1 || links != 1 || grants != 1 {
				t.Fatalf("metadata initialization damaged catalog: roots=%d items=%d files=%d links=%d grants=%d", roots, items, files, links, grants)
			}
			bloemWaitForRunnerConnections(t, control.pool, application, 1)
		})
	}
}

func bloemWaitForRunnerConnections(t *testing.T, control *pgxpool.Pool, application string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var active int
		if err := control.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND datname=current_database()`, application).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("runner left %d owned connections, want %d", active, want)
		case <-ticker.C:
		}
	}
}

func TestBloemLibraryDeletionRunnerBoundsProgressPublication(t *testing.T) {
	control := lifecycleRepo(t)
	job := lifecycleJob(t, control, JobTypeDeleteLibrary)
	claimed, err := control.ClaimNextQueued(t.Context(), JobTypeDeleteLibrary)
	if err != nil || claimed == nil {
		t.Fatalf("claim deletion: %v", err)
	}
	cfg := control.pool.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.MinIdleConns = 0
	var attempts atomic.Int32
	cfg.PrepareConn = func(ctx context.Context, _ *pgx.Conn) (bool, error) {
		if attempts.Add(1) > 1 {
			// Permit the durable update, then hold publication's job read
			// until its own context ends. Both must share the short deadline.
			<-ctx.Done()
			return false, ctx.Err()
		}
		return true, nil
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	runner := NewRunner(NewRepository(pool).withClaim(claimed), nil, nil, nil, nil, nil, nil, nil, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	progress := runner.libraryDeletionProgress(ctx, job.ID)
	progress(1, 5, "Deleting library contents")
	if ctx.Err() != nil {
		t.Fatal("publication consumed the deletion execution deadline")
	}
	for range 100 {
		progress(2, 5, "Later batch")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("timed-out publication was retried %d times, want two total acquisitions", got)
	}
	observed, err := control.GetByID(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if observed.ProgressCurrent != 1 || observed.Message != "Deleting library contents" {
		t.Fatalf("progress write was lost or delayed callbacks mutated it: %d %q", observed.ProgressCurrent, observed.Message)
	}
}
