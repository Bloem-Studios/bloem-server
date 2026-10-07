package scanqueue

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNativeStorageQueueComposition(t *testing.T) {
	pool := &pgxpool.Pool{}
	folders := catalog.NewFolderRepository(pool)
	executor := libraryingest.NewExecutor(nil, nil, folders, nil, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	q := NewService(NewRepository(pool), folders, executor, nil, ctx, 1, 1)
	if !q.NativeStorageComposition(pool, folders, executor) {
		t.Fatal("actual unstarted queue wiring unavailable")
	}
	if q.NativeStorageComposition(pool, folders, libraryingest.NewExecutor(nil, nil, folders, nil, nil, nil)) || q.NativeStorageComposition(&pgxpool.Pool{}, folders, executor) {
		t.Fatal("mismatched queue accepted")
	}
	cancel()
	if q.NativeStorageComposition(pool, folders, executor) {
		t.Fatal("canceled queue advertised admission")
	}
	q = NewService(NewRepository(pool), folders, executor, nil, t.Context(), 1, 1)
	q.Stop()
	if q.NativeStorageComposition(pool, folders, executor) {
		t.Fatal("stopped queue advertised admission")
	}
}
