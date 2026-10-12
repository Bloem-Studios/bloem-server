package libraryingest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

type countedFailureMatcher struct {
	retryRecordingMatcher
	done chan struct{}
	once sync.Once
	err  error
}

func (m *countedFailureMatcher) ProcessBatchByFolderAndPathPrefix(context.Context, int, string, time.Time) (int, error) {
	first := false
	m.once.Do(func() { first = true; close(m.done) })
	if first {
		return 4, m.err
	}
	return 0, nil
}

type countedFailureScanner struct {
	settleStubScanner
	done <-chan struct{}
	err  error
}

func (s *countedFailureScanner) ScanFolder(ctx context.Context, _ *models.MediaFolder) (*scanner.ScanResult, error) {
	select {
	case <-s.done:
	case <-ctx.Done():
	}
	return s.result, s.err
}

type unchangedStorage struct{ mixedStorage }

func (*unchangedStorage) ScanStorageFolder(context.Context, *models.MediaFolder, storagesource.LocationSelection) (*Result, error) {
	return &Result{ScanResult: &scanner.ScanResult{}}, nil
}

type failedSkippedRoots struct{ *skippedRootMemoryRepo }

func (*failedSkippedRoots) DeleteMissingByFolder(context.Context, int, []string) error {
	return errors.New("skipped-root write failed")
}

func TestMixedFailuresKeepCommittedLocalCountsAndPublish(t *testing.T) {
	for _, mode := range []string{"scan-and-drainer", "drainer", "settle", "skipped-roots"} {
		t.Run(mode, func(t *testing.T) {
			matcher := &countedFailureMatcher{done: make(chan struct{}), err: errors.New("matcher failed")}
			local := &countedFailureScanner{settleStubScanner: settleStubScanner{result: &scanner.ScanResult{New: 3}}, done: matcher.done}
			bookkeeping := &mixedBookkeeping{}
			e := &Executor{scanner: local, matcher: matcher, events: bookkeeping, tvDrainSettleWindow: time.Hour}
			folder := &models.MediaFolder{ID: 1, Type: "movies", Paths: []string{"/books"}}
			switch mode {
			case "scan-and-drainer":
				local.err = errors.New("scan failed")
			case "settle":
				folder.Type = "series"
			case "skipped-roots":
				matcher.err = nil
				e.skippedRootRepo = &failedSkippedRoots{newSkippedRootMemoryRepo()}
			}
			e.SetStorageIngestor(&unchangedStorage{mixedStorage{selections: []storagesource.LocationSelection{{Location: storagesource.Location{ID: uuid.New()}}}}})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := e.IngestFolder(ctx, folder)
			if err == nil || result == nil || result.ScanResult.New != 3 || result.MatchedFiles != 4 || result.ScanResult.Errors != 1 || bookkeeping.events != 1 {
				t.Fatalf("result=%+v err=%v events=%d", result, err, bookkeeping.events)
			}
		})
	}
}
