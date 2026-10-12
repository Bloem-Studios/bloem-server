package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

type mixedStorage struct {
	calls      []uuid.UUID
	lookups    int
	fail       uuid.UUID
	selections []storagesource.LocationSelection
}

func (s *mixedStorage) LocationSelections(context.Context, int) ([]storagesource.LocationSelection, error) {
	s.lookups++
	return s.selections, nil
}
func (s *mixedStorage) ScanStorageFolder(_ context.Context, _ *models.MediaFolder, selection storagesource.LocationSelection) (*Result, error) {
	l := selection.Location
	s.calls = append(s.calls, l.ID)
	r := &Result{ScanResult: &scanner.ScanResult{New: 2}}
	if l.ID == s.fail {
		r.ScanResult.Errors = 1
		return r, errors.New("provider failed")
	}
	return r, nil
}
func TestMixedExecutorContinuesIndependentLocations(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	storage := &mixedStorage{fail: a, selections: []storagesource.LocationSelection{{Location: storagesource.Location{ID: a}}, {Location: storagesource.Location{ID: b}}}}
	e := &Executor{scanner: &settleStubScanner{result: &scanner.ScanResult{New: 3}}, matcher: &retryRecordingMatcher{}}
	e.SetStorageIngestor(storage)
	r, err := e.IngestFolder(t.Context(), &models.MediaFolder{ID: 1, Type: "ebooks", Paths: []string{"/books"}})
	if err == nil || len(storage.calls) != 2 || r.ScanResult.New != 7 || r.ScanResult.Errors != 1 {
		t.Fatalf("result=%+v err=%v calls=%v", r, err, storage.calls)
	}
}
func TestMixedSubtreeDoesNotEnumeratePlugins(t *testing.T) {
	storage := &mixedStorage{selections: []storagesource.LocationSelection{{Location: storagesource.Location{ID: uuid.New()}}}}
	e := &Executor{scanner: &settleStubScanner{result: &scanner.ScanResult{}}, matcher: &retryRecordingMatcher{}}
	e.SetStorageIngestor(storage)
	_, err := e.IngestSubtree(t.Context(), &models.MediaFolder{ID: 1, Type: "ebooks", Paths: []string{"/books"}}, "/books/one")
	if err != nil || storage.lookups != 0 {
		t.Fatalf("err=%v lookups=%d", err, storage.lookups)
	}
}

type mixedBookkeeping struct{ updates, events int }

func (b *mixedBookkeeping) UpdateLastScanned(context.Context, int, time.Time) error {
	b.updates++
	return nil
}
func (b *mixedBookkeeping) Publish(context.Context, string, cache.Event) error {
	b.events++
	return nil
}
func (*mixedBookkeeping) Subscribe(context.Context, string, cache.EventHandler) error { return nil }
func (*mixedBookkeeping) Close() error                                                { return nil }
func TestMixedExecutorPublishesAggregateOnce(t *testing.T) {
	storage := &mixedStorage{selections: []storagesource.LocationSelection{{Location: storagesource.Location{ID: uuid.New()}}, {Location: storagesource.Location{ID: uuid.New()}}}}
	bookkeeping := &mixedBookkeeping{}
	e := &Executor{scanner: &settleStubScanner{result: &scanner.ScanResult{New: 3}}, matcher: &retryRecordingMatcher{}, folders: bookkeeping, events: bookkeeping, now: time.Now}
	e.SetStorageIngestor(storage)
	r, err := e.IngestFolder(t.Context(), &models.MediaFolder{ID: 1, Type: "ebooks", Paths: []string{"/books"}})
	if err != nil || r.ScanResult.New != 7 || bookkeeping.events != 1 || bookkeeping.updates != 1 {
		t.Fatalf("result=%+v err=%v bookkeeping=%+v", r, err, bookkeeping)
	}
}

type mixedFailedLocal struct{ settleStubScanner }

func (*mixedFailedLocal) ScanFolder(context.Context, *models.MediaFolder) (*scanner.ScanResult, error) {
	return &scanner.ScanResult{New: 1, Errors: 1}, errors.New("local failed")
}
func TestMixedExecutorContinuesAfterLocalFailure(t *testing.T) {
	storage := &mixedStorage{selections: []storagesource.LocationSelection{{Location: storagesource.Location{ID: uuid.New()}}}}
	e := &Executor{scanner: &mixedFailedLocal{}, matcher: &retryRecordingMatcher{}}
	e.SetStorageIngestor(storage)
	r, err := e.IngestFolder(t.Context(), &models.MediaFolder{ID: 1, Type: "ebooks", Paths: []string{"/books"}})
	if err == nil || len(storage.calls) != 1 || r.ScanResult.New != 3 || r.ScanResult.Errors != 1 {
		t.Fatalf("result=%+v err=%v calls=%v", r, err, storage.calls)
	}
}

type claimedStorage struct {
	e                *Executor
	entered, release chan struct{}
	once             sync.Once
	calls            atomic.Int64
}

func (*claimedStorage) LocationSelections(context.Context, int) ([]storagesource.LocationSelection, error) {
	return []storagesource.LocationSelection{{Location: storagesource.Location{ID: uuid.Nil}}}, nil
}
func (s *claimedStorage) ScanStorageFolder(ctx context.Context, _ *models.MediaFolder, _ storagesource.LocationSelection) (*Result, error) {
	s.e.mu.Lock()
	claims := len(s.e.running)
	s.e.mu.Unlock()
	if claims != 1 {
		return nil, fmt.Errorf("active claims=%d", claims)
	}
	s.calls.Add(1)
	s.once.Do(func() { close(s.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
	}
	return &Result{ScanResult: &scanner.ScanResult{New: 1}}, nil
}
func TestMixedExecutorHoldsOneClaimThroughStorageLane(t *testing.T) {
	e := &Executor{scanner: &settleStubScanner{result: &scanner.ScanResult{}}, matcher: &retryRecordingMatcher{}}
	storage := &claimedStorage{e: e, entered: make(chan struct{}), release: make(chan struct{})}
	e.SetStorageIngestor(storage)
	folder := &models.MediaFolder{ID: 1, Type: "ebooks", Paths: []string{"/books"}}
	done := make(chan error, 2)
	go func() { _, err := e.IngestFolder(t.Context(), folder); done <- err }()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("storage lane did not start")
	}
	waiting := make(chan struct{})
	var once sync.Once
	ctx := WithProgressReporter(t.Context(), func(p ProgressUpdate) {
		if p.Message == "Waiting for an overlapping scan to finish" {
			once.Do(func() { close(waiting) })
		}
	})
	go func() { _, err := e.IngestFolder(ctx, folder); done <- err }()
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("overlapping whole-library scan did not wait")
	}
	if storage.calls.Load() != 1 {
		t.Fatal("second scan entered storage before first released")
	}
	close(storage.release)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("claim did not release")
		}
	}
	if storage.calls.Load() != 2 {
		t.Fatal("waiting scan did not execute")
	}
}
