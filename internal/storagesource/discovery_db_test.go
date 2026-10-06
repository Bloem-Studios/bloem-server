//go:build integration

package storagesource

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDiscoveryRestartsAtCommittedCursor(t *testing.T) { persistedDiscovery(t, 1536, 1) }
func TestBoundedPersistedDiscovery(t *testing.T)          { persistedDiscovery(t, 4096, 2) }

func persistedDiscovery(t *testing.T, total, interruptPages int) {
	t.Helper()
	r, s, lease, _ := scanFixture(t)
	started := time.Now()
	pages := 0
	var peak uint64
	client := providerClient(t, func(_ context.Context, request *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		offset := 0
		var err error
		if request.GetCursor() != "" {
			offset, err = strconv.Atoi(request.GetCursor())
			if err != nil {
				return nil, err
			}
		}
		if offset != pages*512 {
			return nil, status.Error(codes.InvalidArgument, "restart skipped or replayed a page")
		}
		page := &storagev1.ListResponse{}
		for id := offset; id < offset+512 && id < total; id++ {
			page.Entries = append(page.Entries, bookEntry(fmt.Sprintf("book-%06d", id)))
		}
		page.Complete = offset+len(page.Entries) == total
		if !page.Complete {
			page.NextCursor = strconv.Itoa(offset + len(page.Entries))
		}
		pages++
		return page, nil
	})
	for {
		done, err := r.DiscoverPage(context.Background(), lease, client)
		if err != nil {
			t.Fatal(err)
		}
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		if memory.HeapAlloc > peak {
			peak = memory.HeapAlloc
		}
		if done {
			break
		}
		if pages == interruptPages {
			old := lease
			r = NewRepository(r.pool)
			lease, err = r.Begin(context.Background(), s.Key, lease.Owner, time.Minute)
			if err != nil || lease.RunID != old.RunID || lease.Epoch <= old.Epoch {
				t.Fatalf("restart fence: %v", err)
			}
		}
	}
	var count, unique int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*),count(DISTINCT entry_id) FROM bloem_storage_entries WHERE source_key=$1`, s.Key).Scan(&count, &unique); err != nil || count != total || unique != total {
		t.Fatalf("persisted counts %d/%d: %v", count, unique, err)
	}
	var directories, cursors int
	if err := r.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM bloem_storage_scan_directories WHERE run_id=$1 AND complete),(SELECT count(*) FROM bloem_storage_scan_cursors WHERE run_id=$1)`, lease.RunID).Scan(&directories, &cursors); err != nil {
		t.Fatal(err)
	}
	t.Logf("staging entries=%d unique=%d committed_pages=%d terminal_directories=%d restart_after_pages=%d elapsed=%s sampled_host_heap_peak_bytes=%d", count, unique, cursors, directories, interruptPages, time.Since(started), peak)
}

func TestProviderUnavailableDoesNotCompleteOrConfirmAbsence(t *testing.T) {
	failedDiscovery(t, codes.Unavailable, false)
}
func TestRevisionConflictIsNotMissing(t *testing.T) {
	failedDiscovery(t, codes.FailedPrecondition, false)
}
func TestCanceledPageKeepsPreviousCheckpoint(t *testing.T) { failedDiscovery(t, codes.Canceled, true) }

func failedDiscovery(t *testing.T, code codes.Code, cancelPage bool) {
	t.Helper()
	r, _, lease, _ := scanFixture(t)
	fixtureFolder(t, r.pool, 91001)
	execSQL(t, r.pool, `INSERT INTO media_files(id,media_folder_id,file_path,missing_since) VALUES(91001,91001,'/existing/book.epub','2026-01-01T00:00:00Z')`)
	var before string
	if err := r.pool.QueryRow(context.Background(), `SELECT to_jsonb(f)::text FROM media_files f WHERE id=91001`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	client := providerClient(t, func(ctx context.Context, request *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		if request.GetCursor() == "" {
			return &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, NextCursor: "second"}, nil
		}
		if cancelPage {
			close(entered)
			<-ctx.Done()
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, status.Error(code, "fixture interrupted")
	})
	if done, err := r.DiscoverPage(context.Background(), lease, client); done || err != nil {
		t.Fatalf("first page: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if cancelPage {
		go func() {
			select {
			case <-entered:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	if done, err := r.DiscoverPage(ctx, lease, client); done || status.Code(err) != code {
		t.Fatalf("failure changed to completion/missing: %v", err)
	}
	checkpoint, ok, err := NewRepository(r.pool).NextDirectory(context.Background(), lease)
	if err != nil || !ok || checkpoint.Cursor != "second" || checkpoint.Complete {
		t.Fatalf("checkpoint lost: %v", err)
	}
	var count, absent int
	var state, after string
	if err := r.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM bloem_storage_entries),(SELECT count(*) FROM bloem_storage_entries WHERE absence_confirmed_at IS NOT NULL),(SELECT state FROM bloem_storage_scan_runs WHERE id=$1),(SELECT to_jsonb(f)::text FROM media_files f WHERE id=91001)`, lease.RunID).Scan(&count, &absent, &state, &after); err != nil || count != 1 || absent != 0 || state != "running" || after != before {
		t.Fatalf("outage altered availability: %v", err)
	}
}
