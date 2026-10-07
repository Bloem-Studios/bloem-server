//go:build integration

package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type nativeAdmissionResetKey struct{}
type nativeAdmissionTracer struct {
	reads    atomic.Int64
	resetSQL atomic.Pointer[pgconn.PgError]
}

func (q *nativeAdmissionTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	q.reads.Add(1)
	if data.SQL == "DROP SCHEMA public CASCADE; CREATE SCHEMA public" {
		return context.WithValue(ctx, nativeAdmissionResetKey{}, true)
	}
	return ctx
}
func (q *nativeAdmissionTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if ctx.Value(nativeAdmissionResetKey{}) == true {
		var cause *pgconn.PgError
		if errors.As(data.Err, &cause) {
			q.resetSQL.Store(cause)
		}
	}
}

func nativeAdmissionDatabase(t *testing.T) (*pgxpool.Pool, *nativeAdmissionTracer) {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") != "" {
		t.Fatal("SILO_TEST_DATABASE_URL must be unset")
	}
	const private = "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	st, err := os.Stat(private)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private mode-0600 fixture required")
	}
	data, err := os.ReadFile(private)
	if err != nil {
		t.Fatal("read private fixture")
	}
	original, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid private fixture")
	}
	dsn, cleanup, err := bloemtestdb.CloneNativeOnboarding(t.Context(), strings.TrimSpace(string(data)), false)
	if err != nil {
		t.Fatal(err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cleanup(ctx); err != nil {
			t.Error(err)
		} else {
			t.Log("A current UUID clone cleanup verified")
		}
	})
	cfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	if err != nil {
		t.Fatal("unowned clone config")
	}
	tracer := &nativeAdmissionTracer{}
	cfg.ConnConfig.Tracer = tracer
	cfg.MaxConns = 8
	pool, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("open guarded clone")
	}
	var actual string
	if err = pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != cfg.ConnConfig.Database || actual == original.ConnConfig.Database {
		t.Fatal("actual clone identity unverified")
	}
	t.Logf("A current actual UUID clone: %s", actual)
	if err = bloemtestdb.PrepareNativeOnboardingPool(t.Context(), pool); err != nil {
		if cause := tracer.resetSQL.Load(); cause != nil {
			t.Logf("owned clone reset failed SQLSTATE=%s routine=%s", cause.Code, cause.Routine)
		}
		t.Fatal(err)
	}
	if !catalog.NativeStorageSchemaReady(t.Context(), pool) {
		t.Fatal("current schema inventory not ready")
	}
	return pool, tracer
}

func nativeAdmissionLocalFolder(t *testing.T, pool *pgxpool.Pool) *models.MediaFolder {
	t.Helper()
	var id int
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,enabled,owner_id) VALUES('ebook','Admission local',true,bloem_platform_resource_owner_id()) RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return &models.MediaFolder{ID: id, Type: "ebook", Enabled: true}
}

// CURRENT default embedded schema, ordinary local mode only. No initialized
// marker or binding is manufactured to supply a positive native fixture.
func TestNativeOnboardingLiveAdmissionDB(t *testing.T) {
	pool, tracer := nativeAdmissionDatabase(t)
	folder := nativeAdmissionLocalFolder(t, pool)
	s := NewScanner(NewFileRepository(pool), "", nil, 1, false, 0)
	durable, native, err := s.NativeLibraryScanMode(t.Context(), folder.ID)
	if err != nil || durable == nil || durable.ID != folder.ID || native {
		t.Fatal("real same-pool local reader unavailable", err)
	}
	if !s.catalogScanConfigured() || s.nonPersistentParser() {
		t.Fatal("live constructor composition invalid")
	}
	t.Run("validRepairTransactionIsLive", func(t *testing.T) {
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		ctx := withRepairTx(t.Context(), tx)
		before := tracer.reads.Load()
		if err := s.requireLocalLibraryScan(ctx, folder); err != nil {
			t.Fatal("valid carried transaction refused", err)
		}
		if tracer.reads.Load() <= before {
			t.Fatal("valid repair transaction became offline exemption")
		}
	})
	t.Run("typedNilRepairTransactionBeforeReaderAndEffects", func(t *testing.T) {
		var typedNil *onboardingCarriedTx
		before := tracer.reads.Load()
		var progress int
		ctx := withRepairTx(WithProgressReporter(t.Context(), func(ProgressUpdate) { progress++ }), typedNil)
		if _, _, err := s.NativeLibraryScanMode(ctx, folder.ID); err == nil {
			t.Fatal("typed-nil repair transaction admitted by mode reader")
		} else {
			onboardingUnavailable(t, err)
		}
		root := t.TempDir()
		for _, hook := range []string{"folder", "subtree", "file", "ebook"} {
			f := *folder
			f.Paths = []string{root}
			var err error
			switch hook {
			case "folder":
				_, err = s.ScanFolder(ctx, &f)
			case "subtree":
				_, err = s.ScanSubtree(ctx, &f, root)
			case "file":
				err = s.ScanFile(ctx, filepath.Join(root, "absent.epub"), &f)
			case "ebook":
				err = s.ScanEbookFolder(ctx, &f)
			}
			onboardingUnavailable(t, err)
		}
		if tracer.reads.Load() != before || progress != 0 {
			t.Fatal("typed-nil transaction reached reader/watcher/parser effects")
		}
	})
	t.Run("mismatchedRepositoryPool", func(t *testing.T) {
		cfg := pool.Config()
		other, err := pgxpool.NewWithConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal("open alternate pool")
		}
		defer other.Close()
		partial := NewScanner(NewFileRepository(pool), "", nil, 1, false, 0)
		partial.rootSnapshotRepo = NewScannedRootRepository(other)
		before := tracer.reads.Load()
		_, _, err = partial.NativeLibraryScanMode(t.Context(), folder.ID)
		onboardingUnavailable(t, err)
		if tracer.reads.Load() != before {
			t.Fatal("mismatched composition reached mode SQL")
		}
	})
	t.Run("missingSchemaIsTerminal", func(t *testing.T) {
		if _, err := pool.Exec(t.Context(), "ALTER TABLE bloem_native_libraries RENAME TO admission_missing_libraries"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(), "ALTER TABLE admission_missing_libraries RENAME TO bloem_native_libraries"); err != nil {
				t.Error("restore clone table")
			}
		})
		_, _, err := s.NativeLibraryScanMode(t.Context(), folder.ID)
		onboardingUnavailable(t, err)
		_, err = s.ScanFolder(t.Context(), folder)
		onboardingUnavailable(t, err)
	})
}

func TestNativeOnboardingUninitializedStaleScannerRefusalDB(t *testing.T) {
	pool, _ := nativeAdmissionDatabase(t)
	s := NewScanner(NewFileRepository(pool), "", nil, 1, false, 0)
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	state, err := catalog.NewFolderRepository(pool).CreateNativeEbookTx(t.Context(), tx, catalog.NativeLibraryCreate{OwnerID: owner, CreationKey: uuid.New(), Name: "Admission L1"})
	if err != nil {
		t.Fatal("legal L1 create", err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var baselineItems string
	if err = pool.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY content_id)::text,'[]') FROM media_items i").Scan(&baselineItems); err != nil {
		t.Fatal("read post-create catalog baseline")
	}

	var progress int
	ctx := WithProgressReporter(t.Context(), func(ProgressUpdate) { progress++ })
	root := t.TempDir()
	forged := &models.MediaFolder{ID: state.LibraryID, Type: "manga", Enabled: true, Paths: []string{root}}
	for _, hook := range []string{"folder", "subtree", "file", "ebook"} {
		var err error
		switch hook {
		case "folder":
			_, err = s.ScanFolder(ctx, forged)
		case "subtree":
			_, err = s.ScanSubtree(ctx, forged, root)
		case "file":
			err = s.ScanFile(ctx, filepath.Join(root, "absent.epub"), forged)
		case "ebook":
			err = s.ScanEbookFolder(ctx, forged)
		}
		var typed *catalog.NativeOnboardingError
		if !errors.As(err, &typed) || typed.Code != "native_library_not_initialized" {
			t.Fatalf("stale model bypassed durable L1: class=%T", err)
		}
	}
	if progress != 0 {
		t.Fatal("native L1 reached parser effects")
	}
	var items string
	var paths int
	if err = pool.QueryRow(t.Context(), "SELECT (SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY content_id)::text,'[]') FROM media_items i),(SELECT count(*) FROM media_folder_paths WHERE media_folder_id=$1)", state.LibraryID).Scan(&items, &paths); err != nil || items != baselineItems || paths != 0 {
		t.Fatalf("refused L1 scanner changed catalog: error class=%T paths=%d", err, paths)
	}
}
