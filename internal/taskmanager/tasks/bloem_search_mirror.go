package tasks

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
)

// SearchMirrorWorker is the optional Meilisearch search mirror (*catalog.SearchMirror). Its tasks
// are registered only when the mirror is configured.
type SearchMirrorWorker interface {
	RunOnce(ctx context.Context, progress catalog.SearchIndexProgressReporter) error
	ForceFullCopy(ctx context.Context, progress catalog.SearchIndexProgressReporter) error
	StoodDown() bool
}

// SyncCatalogSearchMirrorTask keeps the search mirror in step with the catalog, next to the main
// index's sync task.
type SyncCatalogSearchMirrorTask struct {
	worker SearchMirrorWorker
}

func NewSyncCatalogSearchMirrorTask(w SearchMirrorWorker) *SyncCatalogSearchMirrorTask {
	return &SyncCatalogSearchMirrorTask{worker: w}
}

func (t *SyncCatalogSearchMirrorTask) Key() string  { return "sync_catalog_search_mirror" }
func (t *SyncCatalogSearchMirrorTask) Name() string { return "Sync Catalog Search Mirror" }
func (t *SyncCatalogSearchMirrorTask) Description() string {
	return "Copies the catalog search index to the Meilisearch search mirror when needed, then keeps it in step with catalog changes."
}
func (t *SyncCatalogSearchMirrorTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategoryLibrary
}
func (t *SyncCatalogSearchMirrorTask) IsHidden() bool { return true }
func (t *SyncCatalogSearchMirrorTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return []taskmanager.TriggerConfig{
		{Type: taskmanager.TriggerTypeStartup},
		{Type: taskmanager.TriggerTypeInterval, IntervalMs: 60 * 1000},
	}
}

// ShouldRun skips the scheduled runs of a process that stood down after another Silo copy took
// over search.
func (t *SyncCatalogSearchMirrorTask) ShouldRun(context.Context) (bool, error) {
	if t == nil || t.worker == nil {
		return false, nil
	}
	return !t.worker.StoodDown(), nil
}

func (t *SyncCatalogSearchMirrorTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	if t == nil || t.worker == nil {
		progress.Report(100, "Catalog search mirror is not configured")
		return nil
	}
	if err := t.worker.RunOnce(ctx, progress); err != nil {
		progress.Report(100, "Catalog search mirror sync failed")
		return err
	}
	return nil
}

// RebuildCatalogSearchMirrorTask makes a fresh full copy on the search mirror, as
// rebuild_catalog_search_index does for the main index.
type RebuildCatalogSearchMirrorTask struct {
	worker SearchMirrorWorker
}

func NewRebuildCatalogSearchMirrorTask(w SearchMirrorWorker) *RebuildCatalogSearchMirrorTask {
	return &RebuildCatalogSearchMirrorTask{worker: w}
}

func (t *RebuildCatalogSearchMirrorTask) Key() string  { return "rebuild_catalog_search_mirror" }
func (t *RebuildCatalogSearchMirrorTask) Name() string { return "Rebuild Catalog Search Mirror" }
func (t *RebuildCatalogSearchMirrorTask) Description() string {
	return "Makes a fresh full copy of the catalog search index on the Meilisearch search mirror."
}
func (t *RebuildCatalogSearchMirrorTask) Category() taskmanager.TaskCategory {
	return taskmanager.TaskCategoryLibrary
}
func (t *RebuildCatalogSearchMirrorTask) IsHidden() bool { return true }
func (t *RebuildCatalogSearchMirrorTask) DefaultTriggers() []taskmanager.TriggerConfig {
	return nil
}

func (t *RebuildCatalogSearchMirrorTask) Execute(ctx context.Context, progress taskmanager.ProgressReporter) error {
	if t == nil || t.worker == nil {
		progress.Report(100, "Catalog search mirror is not configured")
		return nil
	}
	if err := t.worker.ForceFullCopy(ctx, progress); err != nil {
		progress.Report(100, "Catalog search mirror copy failed")
		return err
	}
	return nil
}
