package catalog_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminjob"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Use production migrations in a private database, without fixture deletion
// triggers. Those triggers would conceal entitlement foreign-key failures.
func bloemFolderDeletionPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	cfg.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("connect test database administrator")
	}
	name := "bloem_folder_delete_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		if err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	cfg = cfg.Copy()
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 6
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("connect private test database")
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(t.Context(), pool, migrations.BloemFS, "sql"); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestBloemFolderDeletionPreservesBundleReferencedCatalog(t *testing.T) {
	pool := bloemFolderDeletionPool(t)
	ctx := t.Context()
	var folder int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies','Bundle-retained library') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO media_items(content_id,type,title) VALUES('bundle-retained-item','movie','Retained item')`,
		`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES('bundle-retained-item',$1)`,
		`INSERT INTO media_files(content_id,media_folder_id,file_path) VALUES('bundle-retained-item',$1,'/synthetic/retained.mkv')`,
		`INSERT INTO entitlement_bundle_members(bundle_id,bundle_revision,entitlement_kind,root_kind,root_owner_id,media_folder_id)
		 SELECT b.id,b.active_revision,'library_access','media_folder',f.owner_id,f.id
		 FROM entitlement_bundles b CROSS JOIN media_folders f WHERE b.is_organization_creation_default AND f.id=$1`,
	} {
		args := []any{folder}
		if !strings.Contains(query, "$1") {
			args = nil
		}
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The retained reference belongs to a historical revision. It must still
	// prevent physical deletion after a new active revision replaces it.
	if _, err := pool.Exec(ctx, `INSERT INTO entitlement_bundle_versions(bundle_id,revision,created_by_service)
	 SELECT id,active_revision+1,'folder-deletion-test' FROM entitlement_bundles WHERE is_organization_creation_default`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE entitlement_bundles SET active_revision=active_revision+1,status='suspended' WHERE is_organization_creation_default`); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.NewFolderRepository(pool).DeleteWithStats(ctx, folder, nil); !errors.Is(err, catalog.ErrFolderDeletionRestricted) {
		t.Fatalf("bundle-referenced library deletion error = %v", err)
	}
	var items, files, links, grants, members int
	if err := pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM media_items WHERE content_id='bundle-retained-item'),
	 (SELECT count(*) FROM media_files WHERE media_folder_id=$1),
	 (SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1),
	 (SELECT count(*) FROM organization_entitlements WHERE media_folder_id=$1),
	 (SELECT count(*) FROM entitlement_bundle_members WHERE media_folder_id=$1)`, folder).Scan(&items, &files, &links, &grants, &members); err != nil {
		t.Fatal(err)
	}
	if items != 1 || files != 1 || links != 1 || grants != 1 || members != 1 {
		t.Fatalf("blocked deletion damaged catalog/history: items=%d files=%d links=%d grants=%d bundle_members=%d", items, files, links, grants, members)
	}
	handler := handlers.NewLibraryHandler(catalog.NewFolderRepository(pool), nil, nil, pool, nil)
	handler.JobRepo = adminjob.NewRepository(pool)
	_, err := handler.DeleteLibrary(ctx, folder, 0)
	var apiErr *handlers.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Fatalf("delete acceptance error = %v, want conflict", err)
	}
	var enabled bool
	var jobs int
	if err := pool.QueryRow(ctx, `SELECT enabled,(SELECT count(*) FROM admin_jobs) FROM media_folders WHERE id=$1`, folder).Scan(&enabled, &jobs); err != nil {
		t.Fatal(err)
	}
	if !enabled || jobs != 0 {
		t.Fatalf("blocked acceptance disabled library or queued work: enabled=%v jobs=%d", enabled, jobs)
	}
}

func TestBloemFolderDeletionDoesNotConsumeCatalogPool(t *testing.T) {
	pool := bloemFolderDeletionPool(t)
	cfg := pool.Config()
	cfg.MaxConns = 1
	small, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	var folder int
	if err := small.QueryRow(t.Context(), `INSERT INTO media_folders(type,name) VALUES('movies','Single-connection deletion') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := catalog.NewFolderRepository(small).DeleteWithStats(ctx, folder, nil); err != nil {
		t.Fatalf("single-connection catalog could not delete library: %v", err)
	}
	var roots, grants int
	if err := small.QueryRow(t.Context(), `SELECT
	 (SELECT count(*) FROM media_folders WHERE id=$1),
	 (SELECT count(*) FROM organization_entitlements WHERE media_folder_id=$1)`, folder).Scan(&roots, &grants); err != nil {
		t.Fatal(err)
	}
	if roots != 0 || grants != 0 {
		t.Fatalf("root/grants survived deletion: roots=%d grants=%d", roots, grants)
	}
}

func TestBloemFolderDeletionFencesConcurrentBundleReferences(t *testing.T) {
	pool := bloemFolderDeletionPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var folder int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies','Deletion race') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	contender, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = contender.Close(context.Background()) }()
	pid := contender.PgConn().PID()
	result := make(chan error, 1)
	started := false
	_, err = catalog.NewFolderRepository(pool).DeleteWithStats(ctx, folder, func(int, int, string) {
		if started {
			return
		}
		started = true
		go func() {
			_, err := contender.Exec(ctx, `INSERT INTO entitlement_bundle_members(bundle_id,bundle_revision,entitlement_kind,root_kind,root_owner_id,media_folder_id)
			 SELECT b.id,b.active_revision,'library_access','media_folder',f.owner_id,f.id
			 FROM entitlement_bundles b CROSS JOIN media_folders f WHERE b.is_organization_creation_default AND f.id=$1`, folder)
			result <- err
		}()
		// Continue deletion only once the foreign-key insert is observably
		// waiting for the retained root lock, rather than using a fixed sleep.
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			var blocked bool
			if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				return
			}
			select {
			case err := <-result:
				t.Fatalf("bundle insertion was not fenced during deletion: %v", err)
			case <-ctx.Done():
				t.Fatal("bundle insertion never reached the root lock")
			case <-ticker.C:
			}
		}
	})
	if err != nil {
		t.Fatalf("deletion with competing reference: %v", err)
	}
	select {
	case err := <-result:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("reference to deleted library error = %v, want foreign-key rejection", err)
		}
	case <-ctx.Done():
		t.Fatal("competing bundle insert remained blocked after deletion")
	}
}

func TestBloemFolderDeletionDoesNotBlockScannerForeignKeys(t *testing.T) {
	pool := bloemFolderDeletionPool(t)
	ctx := t.Context()
	var folder int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('audiobooks','In-flight scanner') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title) VALUES('scanner-held-item','audiobook','Scanner item')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES('scanner-held-item',$1)`, folder); err != nil {
		t.Fatal(err)
	}
	scanner, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scanner.Close(context.Background()) }()
	scanTx, err := scanner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scanTx.Rollback(context.Background()) }()
	// The scanner's atomic item/file write owns the item before checking a
	// new file's folder FK. Deletion must let it finish instead of owning the
	// folder while a separate batch connection waits for this item.
	if _, err := scanTx.Exec(ctx, `UPDATE media_items SET title=title WHERE content_id='scanner-held-item'`); err != nil {
		t.Fatal(err)
	}
	deleteCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var scannerErr error
	started := false
	_, deleteErr := catalog.NewFolderRepository(pool).DeleteWithStats(deleteCtx, folder, func(int, int, string) {
		if started {
			return
		}
		started = true
		scanCtx, cancelScan := context.WithTimeout(ctx, 2*time.Second)
		defer cancelScan()
		_, scannerErr = scanTx.Exec(scanCtx, `INSERT INTO media_files(content_id,media_folder_id,file_path)
		 VALUES('scanner-held-item',$1,'/synthetic/in-flight.m4b')`, folder)
		if scannerErr == nil {
			scannerErr = scanTx.Commit(scanCtx)
		}
		if scannerErr != nil {
			cancel()
		}
	})
	if scannerErr != nil {
		t.Fatalf("deletion guard blocked scanner FK after it locked an item: %v", scannerErr)
	}
	if deleteErr != nil {
		t.Fatalf("deletion did not finish after the scanner committed: %v", deleteErr)
	}
}

func TestBloemFolderDeletionFencesBundleMoves(t *testing.T) {
	pool := bloemFolderDeletionPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var source, target int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies','Move source') RETURNING id`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies','Move target') RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entitlement_bundle_members(bundle_id,bundle_revision,entitlement_kind,root_kind,root_owner_id,media_folder_id)
	 SELECT b.id,b.active_revision,'library_access','media_folder',f.owner_id,f.id
	 FROM entitlement_bundles b CROSS JOIN media_folders f WHERE b.is_organization_creation_default AND f.id=$1`, source); err != nil {
		t.Fatal(err)
	}
	contender, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = contender.Close(context.Background()) }()
	pid := contender.PgConn().PID()
	result := make(chan error, 1)
	started := false
	_, err = catalog.NewFolderRepository(pool).DeleteWithStats(ctx, target, func(int, int, string) {
		if started {
			return
		}
		started = true
		go func() {
			_, err := contender.Exec(ctx, `UPDATE entitlement_bundle_members SET media_folder_id=$2 WHERE media_folder_id=$1`, source, target)
			result <- err
		}()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			var blocked bool
			if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				return
			}
			select {
			case err := <-result:
				t.Fatalf("bundle move was not fenced: %v", err)
			case <-ctx.Done():
				t.Fatal("bundle move never reached the deletion fence")
			case <-ticker.C:
			}
		}
	})
	if err != nil {
		t.Fatalf("deletion with competing bundle move: %v", err)
	}
	select {
	case err := <-result:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("move to deleted library error = %v, want foreign-key rejection", err)
		}
	case <-ctx.Done():
		t.Fatal("bundle move remained fenced after deletion")
	}
	var preserved int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlement_bundle_members WHERE media_folder_id=$1`, source).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if preserved != 1 {
		t.Fatal("rejected bundle move changed its prior reference")
	}
}

func TestBloemFolderDeletionProgressesWithFencedWriterInSingleConnectionPool(t *testing.T) {
	control := bloemFolderDeletionPool(t)
	cfg := control.Config()
	cfg.MaxConns = 1
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var folder int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies','Occupied catalog pool') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	contenderDone := make(chan error, 1)
	contenderPID := make(chan uint32, 1)
	started := false
	deleteCtx, cancelDelete := context.WithTimeout(ctx, 2*time.Second)
	defer cancelDelete()
	_, deleteErr := catalog.NewFolderRepository(pool).DeleteWithStats(deleteCtx, folder, func(int, int, string) {
		if started {
			return
		}
		started = true
		go func() {
			conn, err := pool.Acquire(ctx)
			if err != nil {
				contenderDone <- err
				return
			}
			defer conn.Release()
			contenderPID <- conn.Conn().PgConn().PID()
			_, err = conn.Exec(ctx, `INSERT INTO entitlement_bundle_members(bundle_id,bundle_revision,entitlement_kind,root_kind,root_owner_id,media_folder_id)
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
			if err := control.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				return
			}
			select {
			case err := <-contenderDone:
				t.Fatalf("bundle insertion was not fenced: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
	})
	if deleteErr != nil {
		t.Fatalf("deletion stalled behind its fence in the occupied catalog pool: %v", deleteErr)
	}
	select {
	case err := <-contenderDone:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("reference to deleted library error = %v, want foreign-key rejection", err)
		}
	case <-ctx.Done():
		t.Fatal("fenced bundle insertion did not finish")
	}
}

func TestBloemFolderDeletionFenceSupportsBigintRoots(t *testing.T) {
	pool := bloemFolderDeletionPool(t)
	// The upstream catalog children intentionally still use int4 folder IDs.
	// Verify the bigint preflight and database fence without that unrelated
	// workflow encoding limit on DeleteWithStats's child-table queries.
	for i, operation := range []string{"insert", "move"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			source, target := (int64(1)<<40)+int64(i*2), (int64(1)<<40)+int64(i*2+1)
			if _, err := pool.Exec(ctx, `INSERT INTO media_folders(id,type,name) VALUES($1,'movies','Bigint source'),($2,'movies','Bigint target')`, source, target); err != nil {
				t.Fatal(err)
			}
			insert := `INSERT INTO entitlement_bundle_members(bundle_id,bundle_revision,entitlement_kind,root_kind,root_owner_id,media_folder_id)
			 SELECT b.id,b.active_revision,'library_access','media_folder',f.owner_id,f.id
			 FROM entitlement_bundles b CROSS JOIN media_folders f WHERE b.is_organization_creation_default AND f.id=$1`
			if _, err := pool.Exec(ctx, insert, source); err != nil {
				t.Fatal(err)
			}
			repo := catalog.NewFolderRepository(pool)
			if err := repo.CheckDeletionAllowed(ctx, int(source)); !errors.Is(err, catalog.ErrFolderDeletionRestricted) {
				t.Fatalf("bigint retained-root preflight = %v", err)
			}
			if err := repo.CheckDeletionAllowed(ctx, int(target)); err != nil {
				t.Fatalf("bigint empty-root preflight = %v", err)
			}
			guard, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = guard.Rollback(context.Background()) }()
			if _, err := guard.Exec(ctx, `SELECT public.bloem_lock_media_folder_bundle_reference($1)`, target); err != nil {
				t.Fatal(err)
			}
			if _, err := guard.Exec(ctx, `SELECT id FROM media_folders WHERE id=$1 FOR NO KEY UPDATE`, target); err != nil {
				t.Fatal(err)
			}
			contender, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = contender.Close(context.Background()) }()
			result := make(chan error, 1)
			go func() {
				query, args := insert, []any{target}
				if operation == "move" {
					query, args = `UPDATE entitlement_bundle_members SET media_folder_id=$2 WHERE media_folder_id=$1`, []any{source, target}
				}
				_, err := contender.Exec(ctx, query, args...)
				result <- err
			}()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var blocked bool
				if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, contender.PgConn().PID()).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("bigint bundle reference was not fenced: %v", err)
				case <-ctx.Done():
					t.Fatal("bigint bundle reference never reached fence")
				case <-ticker.C:
				}
			}
			for _, query := range []string{`DELETE FROM organization_entitlements WHERE media_folder_id=$1`, `DELETE FROM media_folders WHERE id=$1`} {
				if _, err := guard.Exec(ctx, query, target); err != nil {
					t.Fatal(err)
				}
			}
			if err := guard.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
					t.Fatalf("bigint bundle reference outcome = %v, want FK rejection", err)
				}
			case <-ctx.Done():
				t.Fatal("bigint bundle reference remained fenced")
			}
		})
	}
}

func TestBloemFolderDeletionInitializationFailurePreservesCatalog(t *testing.T) {
	for _, stage := range []string{"guard-before-connect", "guard-after-connect", "worker-before-connect", "worker-after-connect"} {
		t.Run(stage, func(t *testing.T) {
			control := bloemFolderDeletionPool(t)
			folder, contentID := bloemDeletionCatalogFixture(t, control)
			cfg := control.Config()
			cfg.MaxConns = 1
			cfg.MinConns = 0
			cfg.MinIdleConns = 0
			application := "bloem-delete-hooks-" + uuid.NewString()
			cfg.ConnConfig.RuntimeParams["application_name"] = application
			forced := errors.New("forced deletion connection configuration failure")
			var before, after atomic.Int32
			cfg.BeforeConnect = func(_ context.Context, _ *pgx.ConnConfig) error {
				n := before.Add(1)
				if (n == 2 && stage == "guard-before-connect") || (n == 3 && stage == "worker-before-connect") {
					return forced
				}
				return nil
			}
			cfg.AfterConnect = func(_ context.Context, _ *pgx.Conn) error {
				n := after.Add(1)
				if (n == 2 && stage == "guard-after-connect") || (n == 3 && stage == "worker-after-connect") {
					return forced
				}
				return nil
			}
			pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if err := pool.Ping(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.NewFolderRepository(pool).DeleteWithStats(t.Context(), folder, nil); !errors.Is(err, forced) {
				t.Fatalf("initialization failure = %v, want configured hook error", err)
			}
			bloemAssertDeletionCatalogPresent(t, control, folder, contentID)
			bloemWaitForDeletionConnections(t, control, application, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			if _, err := catalog.NewFolderRepository(control).DeleteWithStats(ctx, folder, nil); err != nil {
				t.Fatalf("failed initialization retained its guard: %v", err)
			}
		})
	}
}

func TestBloemFolderDeletionCancellationClosesOwnedConnections(t *testing.T) {
	control := bloemFolderDeletionPool(t)
	folder, contentID := bloemDeletionCatalogFixture(t, control)
	cfg := control.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.MinIdleConns = 0
	application := "bloem-delete-cancel-" + uuid.NewString()
	cfg.ConnConfig.RuntimeParams["application_name"] = application
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err = catalog.NewFolderRepository(pool).DeleteWithStats(ctx, folder, func(int, int, string) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled deletion = %v, want context cancellation", err)
	}
	bloemAssertDeletionCatalogPresent(t, control, folder, contentID)
	bloemWaitForDeletionConnections(t, control, application, 1)
	retryCtx, cancelRetry := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelRetry()
	if _, err := catalog.NewFolderRepository(pool).DeleteWithStats(retryCtx, folder, nil); err != nil {
		t.Fatalf("cancelled deletion retained its guard: %v", err)
	}
	bloemWaitForDeletionConnections(t, control, application, 1)
}

func bloemDeletionCatalogFixture(t *testing.T, pool *pgxpool.Pool) (int, string) {
	t.Helper()
	var folder int
	if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name) VALUES('movies','Connection cleanup') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	contentID := "connection-cleanup-" + uuid.NewString()
	for _, query := range []string{
		`INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Connection cleanup item')`,
		`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`,
		`INSERT INTO media_files(content_id,media_folder_id,file_path) VALUES($1,$2,'/synthetic/connection-cleanup.mkv')`,
	} {
		args := []any{contentID, folder}
		if !strings.Contains(query, "$2") {
			args = args[:1]
		}
		if _, err := pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	return folder, contentID
}

func bloemAssertDeletionCatalogPresent(t *testing.T, pool *pgxpool.Pool, folder int, contentID string) {
	t.Helper()
	var roots, items, files, links, grants int
	if err := pool.QueryRow(t.Context(), `SELECT
	 (SELECT count(*) FROM media_folders WHERE id=$1),
	 (SELECT count(*) FROM media_items WHERE content_id=$2),
	 (SELECT count(*) FROM media_files WHERE media_folder_id=$1),
	 (SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1),
	 (SELECT count(*) FROM organization_entitlements WHERE media_folder_id=$1)`, folder, contentID).Scan(&roots, &items, &files, &links, &grants); err != nil {
		t.Fatal(err)
	}
	if roots != 1 || items != 1 || files != 1 || links != 1 || grants != 1 {
		t.Fatalf("failed deletion damaged catalog: roots=%d items=%d files=%d links=%d grants=%d", roots, items, files, links, grants)
	}
}

func bloemWaitForDeletionConnections(t *testing.T, control *pgxpool.Pool, application string, want int) {
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
			t.Fatalf("deletion left %d connections, want %d", active, want)
		case <-ticker.C:
		}
	}
}
