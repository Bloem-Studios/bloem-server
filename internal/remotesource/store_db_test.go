package remotesource

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func remoteSourceTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// The store round-trips a source, replaces a changed URL with a new revision,
// refuses a non-http URL, and the setting reads off until it is "true".
func TestRemoteSourceStoreDB(t *testing.T) {
	pool := remoteSourceTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	path := "/media/movies/remote-source-store-db-test.strm"
	t.Cleanup(func() {
		_ = store.Delete(context.Background(), path)
		_, _ = pool.Exec(context.Background(), `DELETE FROM server_settings WHERE key=$1`, AllowPrivateSettingKey)
	})

	if _, ok, err := store.Get(ctx, path); ok || err != nil {
		t.Fatalf("Get before Put = %v, %v", ok, err)
	}
	first := Source{FilePath: path, URL: "https://media.example.net/a.mkv?token=one", RemoteSize: 1234}
	if err := store.Put(ctx, first); err != nil {
		t.Fatalf("Put = %v", err)
	}
	got, ok, err := store.Get(ctx, path)
	if err != nil || !ok || got.URL != first.URL || got.Revision != Revision(first.URL) || got.RemoteSize != 1234 || got.SourceRevision != "" {
		t.Fatalf("Get = %+v, %v, %v", got, ok, err)
	}
	second := Source{FilePath: path, URL: "https://media.example.net/b.mkv", SourceRevision: "etag-2", RemoteSize: 99}
	if err := store.Put(ctx, second); err != nil {
		t.Fatalf("Put replacement = %v", err)
	}
	got, _, _ = store.Get(ctx, path)
	if got.Revision == Revision(first.URL) || got.Revision != Revision(second.URL) || got.SourceRevision != "etag-2" {
		t.Fatalf("a changed URL must be a new revision: %+v", got)
	}
	if err := store.Put(ctx, Source{FilePath: path, URL: "file:///etc/passwd"}); !errors.Is(err, ErrInvalidStrm) {
		t.Fatalf("Put of a file URL = %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_remote_sources(file_path,url,revision,remote_size) VALUES($1,'gopher://x/','r',0)`, path+"x"); err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM bloem_remote_sources WHERE file_path=$1`, path+"x")
		t.Fatal("the table must refuse a non-http URL")
	}
	if err := store.Delete(ctx, path); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get(ctx, path); ok {
		t.Fatal("Delete left the row")
	}

	if store.AllowPrivate(ctx) {
		t.Fatal("private addresses must be refused by default")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO server_settings(key,value) VALUES($1,'true') ON CONFLICT(key) DO UPDATE SET value='true'`, AllowPrivateSettingKey); err != nil {
		t.Fatal(err)
	}
	if !store.AllowPrivate(ctx) {
		t.Fatal("the setting did not allow private addresses")
	}
}
