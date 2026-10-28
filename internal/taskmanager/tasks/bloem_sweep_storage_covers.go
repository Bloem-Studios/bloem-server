package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/metadata"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

// storageCoverSweepPagesPerRun bounds one run, as artworkSweepPagesPerRun does.
const storageCoverSweepPagesPerRun = 200

// StorageCoverSweepRunner is the storage cover sweep surface. Satisfied by
// *metadata.StorageCoverSweeper; nil when artwork storage is not configured.
type StorageCoverSweepRunner interface {
	Sweep(ctx context.Context, maxPages int) (metadata.StorageCoverSweepStats, error)
}

// SweepStorageCoversTask deletes storage covers kept in artwork storage that
// no book names any more: a removed book's, or one its source replaced.
type SweepStorageCoversTask struct {
	runner StorageCoverSweepRunner
}

func NewSweepStorageCoversTask(runner StorageCoverSweepRunner) *SweepStorageCoversTask {
	return &SweepStorageCoversTask{runner: runner}
}

func (*SweepStorageCoversTask) Key() string  { return "bloem_sweep_storage_covers" }
func (*SweepStorageCoversTask) Name() string { return "Sweep Kept Storage Covers" }
func (*SweepStorageCoversTask) Description() string {
	return "Deletes book covers kept in artwork storage that no book uses any more, such as a removed book's or one its source replaced"
}
func (*SweepStorageCoversTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategoryMetadata
}
func (*SweepStorageCoversTask) IsHidden() bool { return false }

// DefaultTriggers runs the sweep daily, as the artwork storage sweep runs.
func (*SweepStorageCoversTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{
		{Type: taskmanager.TriggerTypeInterval, IntervalMs: int64((24 * time.Hour) / time.Millisecond)},
	}
}

func (t *SweepStorageCoversTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	if t.runner == nil {
		progress.Report(100, "Storage cover sweep is not configured")
		return nil
	}
	progress.Report(5, "Sweeping kept storage covers")
	stats, err := t.runner.Sweep(ctx, storageCoverSweepPagesPerRun)
	if err != nil {
		return fmt.Errorf("sweeping kept storage covers: %w", err)
	}
	progress.Report(100, fmt.Sprintf("Scanned %d kept covers, deleted %d", stats.Scanned, stats.Deleted))
	return nil
}
