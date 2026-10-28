package metadata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/blobstore"
)

func TestStorageCoverSweepDeletesCoversNoBookNames(t *testing.T) {
	store, err := blobstore.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	current := artworkkey.StorageCoverKey("book-1", "cover/book-1", "r2")
	replaced := artworkkey.StorageCoverKey("book-1", "cover/book-1", "r1")
	removed := artworkkey.StorageCoverKey("book-2", "cover/book-2", "r1")
	unparsable := "storage-covers/book-3/not-a-cover"
	for _, key := range []string{current, replaced, removed, unparsable} {
		if err := store.Put(ctx, key, []byte("cover")); err != nil {
			t.Fatal(err)
		}
	}
	sweeper := &StorageCoverSweeper{store: store, now: time.Now, posters: func(_ context.Context, ids []string) (map[string]string, error) {
		return map[string]string{"book-1": artworkkey.StorageCoverPath("book-1", "cover/book-1", "r2")}, nil
	}}
	// Covers kept within the last day are left alone.
	stats, err := sweeper.Sweep(ctx, 10)
	if err != nil || stats.Deleted != 0 || stats.TooNew != 3 || stats.Unparsable != 1 {
		t.Fatalf("young covers: %+v %v", stats, err)
	}
	sweeper.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	stats, err = sweeper.Sweep(ctx, 10)
	if err != nil || stats.Deleted != 2 || stats.Referenced != 1 || stats.Unparsable != 1 {
		t.Fatalf("sweep: %+v %v", stats, err)
	}
	for key, kept := range map[string]bool{current: true, replaced: false, removed: false, unparsable: true} {
		_, err := store.Stat(ctx, key)
		if got := err == nil; got != kept {
			t.Errorf("%s kept=%v err=%v", key, got, err)
		}
	}
	// A failed catalog lookup deletes nothing.
	sweeper.posters = func(context.Context, []string) (map[string]string, error) { return nil, errors.New("down") }
	if err := store.Put(ctx, removed, []byte("cover")); err != nil {
		t.Fatal(err)
	}
	if _, err := sweeper.Sweep(ctx, 10); err == nil {
		t.Fatal("lookup failure ignored")
	}
	if _, err := store.Stat(ctx, removed); err != nil {
		t.Fatalf("cover deleted without a catalog answer: %v", err)
	}
}
