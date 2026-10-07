package storagesource

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Each DB test gets its own database, cloned from one migrated template per
// test binary; some tests alter tables to inject failures.
var (
	templateOnce sync.Once
	templateName string
	templateErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if templateName != "" {
		if admin, err := maintenanceConn(context.Background()); err == nil {
			_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{templateName}.Sanitize()+" WITH (FORCE)")
			_ = admin.Close(context.Background())
		}
	}
	os.Exit(code)
}

func maintenanceConn(ctx context.Context) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	cfg.Database = "postgres"
	return pgx.ConnectConfig(ctx, cfg)
}

func databaseURL(name string) (string, error) {
	u, err := url.Parse(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		return "", err
	}
	u.Path, u.RawPath = "/"+name, ""
	return u.String(), nil
}

func storageTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	templateOnce.Do(func() {
		name := fmt.Sprintf("storagesource_test_template_%d", time.Now().UnixNano())
		admin, err := maintenanceConn(context.Background())
		if err != nil {
			templateErr = err
			return
		}
		defer func() { _ = admin.Close(context.Background()) }()
		if _, err = admin.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
			templateErr = err
			return
		}
		templateName = name
		dsn, err := databaseURL(name)
		if err != nil {
			templateErr = err
			return
		}
		templateErr = bloemtestdb.Prepare(context.Background(), dsn, bloemtestdb.Options{})
	})
	if templateErr != nil {
		t.Fatal(templateErr)
	}
	admin, err := maintenanceConn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	name := "storagesource_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{templateName}.Sanitize())
	_ = admin.Close(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := databaseURL(name)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if admin, err := maintenanceConn(context.Background()); err == nil {
			_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			_ = admin.Close(context.Background())
		}
	})
	return pool
}

func execSQL(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func fixtureFolder(t *testing.T, pool *pgxpool.Pool, id int) {
	t.Helper()
	execSQL(t, pool, `INSERT INTO media_folders(id,type,name) VALUES($1,'ebooks','Storage test')`, id)
}

func fixtureSource(t *testing.T, pool *pgxpool.Pool) (SourceConfig, *Repository) {
	t.Helper()
	r := NewRepository(pool)
	s, err := r.CreateSource(context.Background(), SourceConfig{PluginID: "fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, r
}

func fixtureLocation(t *testing.T, r *Repository, sourceKey uuid.UUID, folderID int) Location {
	t.Helper()
	tx, err := r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	location, err := r.AddLocationTx(t.Context(), tx, sourceKey, folderID)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return location
}

func attachFile(t *testing.T, r *Repository, configurationRevision int64, fileID int, ref PersistedRef) error {
	t.Helper()
	tx, err := r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = AttachFilesTx(t.Context(), tx, configurationRevision, []FileRef{{MediaFileID: fileID, PersistedRef: ref}}); err != nil {
		return err
	}
	return tx.Commit(t.Context())
}

// discoverPage lists and records one page of the next pending directory, as
// a storage scan does, and reports whether discovery has finished.
func discoverPage(r *Repository, ctx context.Context, lease Lease, client storagev1.StorageProviderClient) (bool, error) {
	checkpoint, pending, err := r.NextDirectory(ctx, lease)
	if err != nil {
		return false, err
	}
	if !pending {
		err = r.Complete(ctx, lease)
		return err == nil, err
	}
	page, err := r.FetchPage(ctx, lease, checkpoint, client)
	if err != nil {
		return false, err
	}
	return false, r.ApplyPage(ctx, lease, checkpoint, page, nil)
}
