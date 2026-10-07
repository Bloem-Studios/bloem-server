//go:build integration

package libraryingest

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeExecutorDatabase(t *testing.T) *pgxpool.Pool {
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
		t.Fatal(err)
	}
	if !catalog.NativeStorageSchemaReady(t.Context(), pool) {
		t.Fatal("current schema inventory not ready")
	}
	return pool
}

// Actual CURRENT installed mode readers, with synthetic collaborators used
// only to observe refusal. No synthetic reader grants a native success.
func TestNativeOnboardingExecutorLiveModeDB(t *testing.T) {
	pool := nativeExecutorDatabase(t)
	folders := catalog.NewFolderRepository(pool)
	var id int
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('ebook','Executor local',bloem_platform_resource_owner_id()) RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	realScanner := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0)
	f := &models.MediaFolder{ID: id, Type: "movies", Paths: []string{"/forged"}}
	for _, e := range []*Executor{NewExecutor(nil, nil, folders, nil, nil, nil), NewExecutor(realScanner, nil, nil, nil, nil, nil)} {
		result, handled, err := e.tryNativeModeIngest(t.Context(), f, scopeModeLibrary)
		if err != nil || handled || result != nil {
			t.Fatal("real local classification did not preserve local pipeline", err)
		}
	}
	spy := &onboardingScanSpy{}
	matcher := &retryRecordingMatcher{}
	native := &onboardingNativeSpy{bound: true}
	e := NewExecutor(spy, matcher, folders, nil, nil, nil)
	e.SetNativeIngestor(native)
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	state, err := folders.CreateNativeEbookTx(t.Context(), tx, catalog.NativeLibraryCreate{OwnerID: owner, CreationKey: uuid.New(), Name: "Executor L1"})
	if err != nil {
		t.Fatal("legal L1 create", err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var progress int
	ctx := WithProgressReporter(t.Context(), func(ProgressUpdate) { progress++ })
	_, err = e.IngestFolder(ctx, &models.MediaFolder{ID: state.LibraryID, Type: "music", Paths: []string{"/forged"}})
	var typed *catalog.NativeOnboardingError
	if !errors.As(err, &typed) || typed.Code != "native_library_not_initialized" {
		t.Fatalf("actual L1 mode not terminal: class=%T", err)
	}
	if _, err = pool.Exec(t.Context(), "ALTER TABLE bloem_native_libraries RENAME TO executor_missing_libraries"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "ALTER TABLE executor_missing_libraries RENAME TO bloem_native_libraries"); err != nil {
			t.Error("restore clone table")
		}
	})
	_, err = e.IngestFolder(ctx, f)
	if !errors.As(err, &typed) || typed.Code != "native_storage_unavailable" {
		t.Fatalf("actual missing schema not terminal: class=%T", err)
	}
	if spy.calls != 0 || spy.modeCalls != 0 || native.calls != 0 || progress != 0 || matcher.processAllCalls.Load() != 0 || matcher.retryCalls.Load() != 0 || len(e.running) != 0 {
		t.Fatal("selected live mode error fell back or acquired local/native effects")
	}
}
