package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// fakeAudioJournal hands out pending directories in order, like
// pendingDirectoryTx, and queues children when a parent's page is applied.
type fakeAudioJournal struct {
	mu       sync.Mutex
	pending  []string
	children map[string][]string
}

func (j *fakeAudioJournal) NextDirectoryExcluding(_ context.Context, _ storagesource.Lease, inFlight []string) (storagesource.Checkpoint, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, id := range j.pending {
		if !slices.Contains(inFlight, id) {
			return storagesource.Checkpoint{DirectoryID: id}, true, nil
		}
	}
	return storagesource.Checkpoint{}, false, nil
}

func (j *fakeAudioJournal) apply(id string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pending = slices.DeleteFunc(j.pending, func(p string) bool { return p == id })
	j.pending = append(j.pending, j.children[id]...)
	slices.Sort(j.pending)
}

func TestAudioPipelineAppliesEveryDirectoryOnceAndFinishes(t *testing.T) {
	journal := &fakeAudioJournal{pending: []string{"root"}, children: map[string][]string{}}
	for i := range 20 {
		journal.children["root"] = append(journal.children["root"], fmt.Sprintf("book-%02d", i))
	}
	var concurrent, peak atomic.Int32
	// Book fetches wait until two are in flight, so the test fails rather
	// than passes by luck if workers never overlap.
	overlap := make(chan struct{})
	var overlapped sync.Once
	fetching := sync.Map{}
	fetch := func(ctx context.Context, cp storagesource.Checkpoint) (*storagev1.ListResponse, error) {
		if _, loaded := fetching.LoadOrStore(cp.DirectoryID, true); loaded {
			t.Errorf("%s fetched by two workers at once", cp.DirectoryID)
		}
		defer fetching.Delete(cp.DirectoryID)
		now := concurrent.Add(1)
		defer concurrent.Add(-1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		if now >= 2 {
			overlapped.Do(func() { close(overlap) })
		}
		if cp.DirectoryID != "root" {
			select {
			case <-overlap:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &storagev1.ListResponse{Complete: true}, nil
	}
	prepare := func(context.Context, storagesource.Checkpoint, *storagev1.ListResponse) ([]scanner.StorageAudioTrack, int, error) {
		return nil, 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := startAudioPipeline(ctx, 4, journal, storagesource.Lease{}, fetch, prepare)
	defer p.stop()
	var applied []string
	for {
		item, more, err := p.next()
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
		journal.apply(item.checkpoint.DirectoryID)
		applied = append(applied, item.checkpoint.DirectoryID)
		p.release(item.checkpoint.DirectoryID)
	}
	if len(applied) != 21 || applied[0] != "root" {
		t.Fatalf("applied %d directories, first %v", len(applied), applied[:min(len(applied), 1)])
	}
	slices.Sort(applied)
	if len(slices.Compact(applied)) != 21 {
		t.Fatal("a directory was applied twice")
	}
	if peak.Load() < 2 {
		t.Fatalf("workers did not overlap (peak %d)", peak.Load())
	}
}

func TestAudioPipelineStopsAtTheFirstFailure(t *testing.T) {
	journal := &fakeAudioJournal{pending: []string{"a", "b", "c"}, children: map[string][]string{}}
	fetch := func(_ context.Context, cp storagesource.Checkpoint) (*storagev1.ListResponse, error) {
		if cp.DirectoryID == "b" {
			return nil, errors.New("provider failed")
		}
		return &storagev1.ListResponse{Complete: true}, nil
	}
	prepare := func(context.Context, storagesource.Checkpoint, *storagev1.ListResponse) ([]scanner.StorageAudioTrack, int, error) {
		return nil, 0, nil
	}
	p := startAudioPipeline(context.Background(), 2, journal, storagesource.Lease{}, fetch, prepare)
	defer p.stop()
	for {
		item, more, err := p.next()
		if err != nil {
			return
		}
		if !more {
			t.Fatal("finished without reporting the failure")
		}
		journal.apply(item.checkpoint.DirectoryID)
		p.release(item.checkpoint.DirectoryID)
	}
}
