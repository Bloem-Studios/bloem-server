package storagesource

import (
	"context"
	"sync"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// scanAll runs one complete discovery of the source and returns the entry IDs
// each publication saw as removed.
func scanAll(t *testing.T, r *Repository, s SourceConfig, client storagev1.StorageProviderClient) []string {
	t.Helper()
	ctx := context.Background()
	lease, err := r.Begin(ctx, s.Key, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var removed []string
	for {
		checkpoint, pending, err := r.NextDirectory(ctx, lease)
		if err != nil {
			t.Fatal(err)
		}
		if !pending {
			if err := r.Complete(ctx, lease); err != nil {
				t.Fatal(err)
			}
			return removed
		}
		page, err := r.FetchPage(ctx, lease, checkpoint, client)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.ApplyPage(ctx, lease, checkpoint, page, func(_ context.Context, _ pgx.Tx, _ []*storagev1.Entry, gone []string) error {
			removed = append(removed, gone...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChangeFeedListsOnlyChangesAfterACompleteListing(t *testing.T) {
	pool := storageTestPool(t)
	s, r := fixtureSource(t, pool)
	var mu sync.Mutex
	var requests []string
	client := providerClient(t, func(_ context.Context, req *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		mu.Lock()
		requests = append(requests, req.GetChangesSince())
		mu.Unlock()
		switch req.GetChangesSince() {
		case "":
			return &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("a"), bookEntry("b")}, Complete: true, ChangeToken: "t1"}, nil
		case "t1":
			return &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("c")}, RemovedEntryIds: []string{"b"}, Complete: true, ChangeToken: "t2"}, nil
		default:
			return nil, status.Error(codes.FailedPrecondition, "change token expired")
		}
	})

	if removed := scanAll(t, r, s, client); len(removed) != 0 {
		t.Fatalf("full listing removed %v", removed)
	}
	if removed := scanAll(t, r, s, client); len(removed) != 1 || removed[0] != "b" {
		t.Fatalf("incremental listing removed %v", removed)
	}
	var token string
	if err := pool.QueryRow(t.Context(), `SELECT token FROM bloem_storage_change_tokens WHERE source_key=$1 AND directory_id='root'`, s.Key).Scan(&token); err != nil || token != "t2" {
		t.Fatalf("token = %q %v", token, err)
	}
	// "t2" is refused: the scan falls back to a full listing and starts over.
	if removed := scanAll(t, r, s, client); len(removed) != 0 {
		t.Fatalf("fallback listing removed %v", removed)
	}
	if err := pool.QueryRow(t.Context(), `SELECT token FROM bloem_storage_change_tokens WHERE source_key=$1 AND directory_id='root'`, s.Key).Scan(&token); err != nil || token != "t1" {
		t.Fatalf("token after fallback = %q %v", token, err)
	}
	want := []string{"", "t1", "t2", ""}
	if len(requests) != len(want) {
		t.Fatalf("requests = %q", requests)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("requests = %q, want %q", requests, want)
		}
	}

	// A stale token, or one from another configuration, is never sent.
	execSQL(t, pool, `UPDATE bloem_storage_change_tokens SET issued_at = now() - interval '8 days' WHERE source_key=$1`, s.Key)
	requests = nil
	scanAll(t, r, s, client)
	if len(requests) != 1 || requests[0] != "" {
		t.Fatalf("expired token sent: %q", requests)
	}
}
