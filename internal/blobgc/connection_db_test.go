package blobgc_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/blobgc"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/chapterthumbs"
	"github.com/Silo-Server/silo-server/internal/trickplay"
)

// The tests here pin what a collector or sweeper does with one database
// connection, and bound it so a regression that needs a second connection
// fails the test instead of hanging it. Everything a test does before that
// behavior is setup: writing the objects (each write syncs the file and its
// directory), connecting, queueing rows. Setup waits on the disk and on
// Postgres, which a loaded runner shares with every other package's tests, so
// it has its own generous bound and never draws on the behavior's. The
// behavior's bound only guards a hang: a collector that waits for a second
// connection never finishes, whatever the bound.
const (
	setupTimeout    = time.Minute
	behaviorTimeout = 30 * time.Second
)

// within returns a context that ends d from now, or with the test.
func within(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}

func singleConnectionPool(t *testing.T, prefixes []string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns, cfg.MinConns = 1, 0
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), setupTimeout)
		defer cancel()
		if _, err := pool.Exec(ctx, `DELETE FROM public.blob_gc_queue WHERE prefix = ANY($1::text[])`, prefixes); err != nil {
			t.Error(err)
		}
	})
	// The pool opens its one connection on first use. Open it here, inside the
	// setup bound, so the first query a test times is not the one that connects
	// to a freshly copied database.
	if err := pool.Ping(within(t, setupTimeout)); err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	return pool
}

// collectFirst makes the collector claim these queue rows before any other.
// Collect claims the oldest due row whoever queued it, and a database that
// several packages' tests share (the Go test shards run four packages at once
// on one) can hold a foreign due row. It would take one of the limited claims
// and leave a test that counts its own deletions one short.
func collectFirst(t *testing.T, ctx context.Context, pool *pgxpool.Pool, prefixes []string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE public.blob_gc_queue SET not_before = '-infinity' WHERE prefix = ANY($1::text[])`, prefixes); err != nil {
		t.Fatal(err)
	}
}

func assertCollected(t *testing.T, ctx context.Context, store *blobstore.Filesystem, keys []string) {
	t.Helper()
	for _, key := range keys {
		body, _, err := store.Get(ctx, key)
		if body != nil {
			_ = body.Close()
		}
		if !errors.Is(err, blobstore.ErrNotFound) {
			t.Errorf("object %s remains after collection: %v", key, err)
		}
	}
}

func TestCollectorUsesOneConnectionDB(t *testing.T) {
	id := time.Now().UnixNano()
	prefixes := []string{
		fmt.Sprintf("chapter-images/%d/", id),
		fmt.Sprintf("chapter-images/%d/0/w300.webp", id+1),
		fmt.Sprintf("trickplay/%d/1/", id+2),
	}
	keys := []string{prefixes[0] + "0/w300.webp", prefixes[1], prefixes[2] + "0.1.jpg"}
	pool := singleConnectionPool(t, prefixes)
	store, err := blobstore.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setup := within(t, setupTimeout)
	for _, key := range keys {
		if err := store.Put(setup, key, []byte("image")); err != nil {
			t.Fatal(err)
		}
	}
	if err := blobgc.NewQueue(pool).Schedule(setup, prefixes, 0); err != nil {
		t.Fatal(err)
	}
	collectFirst(t, setup, pool, prefixes)
	collector := blobgc.NewCollector(pool, store,
		chapterthumbs.ImageBlobNamespace(), chapterthumbs.BlobNamespace(), trickplay.BlobNamespace())
	ctx := within(t, behaviorTimeout)
	stats, err := collector.Collect(ctx, len(prefixes))
	if err != nil || stats.Deleted != len(prefixes) || stats.Objects != len(keys) {
		t.Fatalf("single-connection collection: stats=%+v err=%v", stats, err)
	}
	assertCollected(t, ctx, store, keys)
}

func TestSweeperUsesOneConnectionDB(t *testing.T) {
	id := time.Now().UnixNano()
	prefixes := []string{fmt.Sprintf("chapter-images/%d/", id), fmt.Sprintf("trickplay/%d/1/", id+1)}
	keys := []string{prefixes[0] + "0/w300.webp", prefixes[1] + "0.1.jpg"}
	pool := singleConnectionPool(t, prefixes)
	root := t.TempDir()
	store, err := blobstore.NewFilesystem(root)
	if err != nil {
		t.Fatal(err)
	}
	setup := within(t, setupTimeout)
	old := time.Now().Add(-48 * time.Hour)
	for _, key := range keys {
		if err := store.Put(setup, key, []byte("image")); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(root, key), old, old); err != nil {
			t.Fatal(err)
		}
	}
	namespaces := []blobgc.Namespace{chapterthumbs.BlobNamespace(), trickplay.BlobNamespace()}
	ctx := within(t, behaviorTimeout)
	for run := range 2 {
		stats, err := blobgc.NewSweeper(pool, store, namespaces...).Sweep(ctx, 1)
		if err != nil || len(stats.Namespaces) != len(namespaces) || stats.Skipped {
			t.Fatalf("single-connection sweep: stats=%+v err=%v", stats, err)
		}
		for _, ns := range stats.Namespaces {
			if run == 0 && ns.Queued != 1 || run == 1 && ns.Scheduled != 1 {
				t.Fatalf("sweep %d: %+v", run, ns)
			}
		}
	}
	collectFirst(t, within(t, setupTimeout), pool, prefixes)
	ctx = within(t, behaviorTimeout)
	stats, err := blobgc.NewCollector(pool, store, namespaces...).Collect(ctx, len(prefixes))
	if err != nil || stats.Deleted != len(prefixes) {
		t.Fatalf("collect swept prefixes: stats=%+v err=%v", stats, err)
	}
	assertCollected(t, ctx, store, keys)
}
