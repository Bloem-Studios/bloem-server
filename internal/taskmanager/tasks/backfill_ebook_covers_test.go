package tasks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/scanner"
)

type stubEbookCoverBackfiller struct {
	stats  scanner.EbookCoverBackfillStats
	err    error
	calls  int
	limit  int
	budget time.Duration
}

func (s *stubEbookCoverBackfiller) BackfillMissingEbookCovers(_ context.Context, limit int, budget time.Duration) (scanner.EbookCoverBackfillStats, error) {
	s.calls++
	s.limit = limit
	s.budget = budget
	return s.stats, s.err
}

// The sweep is bounded by both a page size and a wall-clock budget, because a
// library on slow or remote storage can spend the whole task lane on one page.
// A task that forwarded neither would read until it finished or the server
// restarted.
func TestBackfillEbookCoversTaskPassesItsBounds(t *testing.T) {
	backfiller := &stubEbookCoverBackfiller{}
	task := NewBackfillEbookCoversTask(backfiller)

	if err := task.Execute(context.Background(), &fakeProgress{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if backfiller.calls != 1 {
		t.Fatalf("backfiller calls = %d, want 1", backfiller.calls)
	}
	if backfiller.limit != backfillEbookCoversClaimLimit {
		t.Errorf("limit = %d, want %d", backfiller.limit, backfillEbookCoversClaimLimit)
	}
	if backfiller.budget != backfillEbookCoversBudget {
		t.Errorf("budget = %v, want %v", backfiller.budget, backfillEbookCoversBudget)
	}
}

// The task runs on an interval forever, so the steady state -- every book either
// has artwork or has already been examined -- has to read as success rather than
// as a run that found nothing.
func TestBackfillEbookCoversTaskReportsAnIdleSweepPlainly(t *testing.T) {
	task := NewBackfillEbookCoversTask(&stubEbookCoverBackfiller{})
	progress := &fakeProgress{}

	if err := task.Execute(context.Background(), progress); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(progress.lastMessage, "already been examined") {
		t.Errorf("idle message = %q, want it to say there was nothing left to examine", progress.lastMessage)
	}
}

func TestBackfillEbookCoversTaskReportsWhatItDid(t *testing.T) {
	task := NewBackfillEbookCoversTask(&stubEbookCoverBackfiller{stats: scanner.EbookCoverBackfillStats{
		Examined:       10,
		Applied:        7,
		Absent:         2,
		Unreadable:     1,
		Remaining:      42,
		RemainingKnown: true,
	}})
	progress := &fakeProgress{}

	if err := task.Execute(context.Background(), progress); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"Examined 10", "7 covers applied", "2 with no cover", "1 unreadable", "42 left"} {
		if !strings.Contains(progress.lastMessage, want) {
			t.Errorf("message %q missing %q", progress.lastMessage, want)
		}
	}
}

// A sweep that could not look is not a sweep that found nothing: the task must
// fail so the run is recorded as failed rather than as a clean idle pass.
func TestBackfillEbookCoversTaskFailsWhenTheSweepCannotRun(t *testing.T) {
	wantErr := errors.New("selecting candidates failed")
	task := NewBackfillEbookCoversTask(&stubEbookCoverBackfiller{err: wantErr})

	err := task.Execute(context.Background(), &fakeProgress{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute error = %v, want %v", err, wantErr)
	}
}

// Registration is unconditional in main.go only because a nil scanner is
// filtered there; a nil backfiller reaching Execute must still be inert.
func TestBackfillEbookCoversTaskIsInertWithoutABackfiller(t *testing.T) {
	task := NewBackfillEbookCoversTask(nil)
	if err := task.Execute(context.Background(), &fakeProgress{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}
