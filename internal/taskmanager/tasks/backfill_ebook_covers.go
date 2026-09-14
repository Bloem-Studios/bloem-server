package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

// The ebook scanner extracts a cover from the book itself, but only while
// reconciling a file whose size or mtime changed. Books ingested before that
// path was reachable sit behind the scanner's unchanged-skip and are never
// revisited, however often the library is rescanned.
//
// This task sweeps them. It runs on an interval rather than manual-only,
// unlike BackfillMetadataImagesTask: that one discovers work across the whole
// catalog and costs provider bandwidth, so it waits for an administrator, while
// this one reads files already on disk and converges to nothing once every book
// has been examined. scanner.BackfillMissingEbookCovers records the books that
// hold no cover, so a steady-state library selects zero candidates and the
// interval costs one indexed query.

const (
	// backfillEbookCoversInterval paces the sweep once the initial backlog is
	// gone. A library that gains ebooks between scans gets its covers within a
	// few hours; one that does not pays a single count query.
	backfillEbookCoversInterval = 6 * time.Hour

	// backfillEbookCoversClaimLimit is how many books one execution examines.
	// Each is a file open and, for an EPUB, a manifest parse plus one image
	// read, so a page is bounded work on the scanner's own I/O path rather than
	// a provider's. A 166k backlog drains in roughly a week of six-hourly runs,
	// which is the right trade against competing with live scans.
	backfillEbookCoversClaimLimit = 2000

	// backfillEbookCoversBudget stops an execution that is reading slow media,
	// so a page of large or remote files cannot hold the task lane open
	// indefinitely. The next run resumes from the same candidate ordering.
	backfillEbookCoversBudget = 10 * time.Minute
)

// EbookCoverBackfiller extracts covers for ebooks that have none. Satisfied by
// *scanner.Scanner.
type EbookCoverBackfiller interface {
	BackfillMissingEbookCovers(ctx context.Context, limit int, budget time.Duration) (scanner.EbookCoverBackfillStats, error)
}

type BackfillEbookCoversTask struct {
	backfiller EbookCoverBackfiller
	limit      int
	budget     time.Duration
}

func NewBackfillEbookCoversTask(backfiller EbookCoverBackfiller) *BackfillEbookCoversTask {
	return &BackfillEbookCoversTask{
		backfiller: backfiller,
		limit:      backfillEbookCoversClaimLimit,
		budget:     backfillEbookCoversBudget,
	}
}

func (t *BackfillEbookCoversTask) Key() string  { return "backfill_ebook_covers" }
func (t *BackfillEbookCoversTask) Name() string { return "Backfill Ebook Covers" }
func (t *BackfillEbookCoversTask) Description() string {
	return "Extracts cover art from ebooks that have none, using sidecar images and embedded covers"
}
func (t *BackfillEbookCoversTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategoryLibrary
}
func (t *BackfillEbookCoversTask) IsHidden() bool { return false }

func (t *BackfillEbookCoversTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{
		{Type: taskmanager.TriggerTypeInterval, IntervalMs: backfillEbookCoversInterval.Milliseconds()},
	}
}

func (t *BackfillEbookCoversTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	if t.backfiller == nil {
		progress.Report(100, "Ebook cover backfill is not configured")
		return nil
	}
	progress.Report(0, "Finding ebooks without cover art")

	stats, err := t.backfiller.BackfillMissingEbookCovers(ctx, t.limit, t.budget)
	if err != nil {
		return fmt.Errorf("backfilling ebook covers: %w", err)
	}

	progress.Report(100, formatEbookCoverBackfillResult(stats))
	return nil
}

func formatEbookCoverBackfillResult(stats scanner.EbookCoverBackfillStats) string {
	if stats.Examined == 0 {
		return "Every ebook either has cover art or has already been examined"
	}
	message := fmt.Sprintf(
		"Examined %d ebooks: %d covers applied, %d with no cover to find, %d unreadable",
		stats.Examined,
		stats.Applied,
		stats.Absent,
		stats.Unreadable,
	)
	if stats.RemainingKnown {
		message += fmt.Sprintf(", %d left to examine", stats.Remaining)
	}
	return message
}
