package catalog

// Placeholder for Task 8 of the search mirror plan, so parallel tasks compile.
// The Task 8 implementer deletes this file and writes the real code.

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewSearchMirror(pool *pgxpool.Pool, cfg SearchMirrorConfig, settings SettingsStore, startup CatalogSearchSettings, docs searchMirrorDocuments, opts SearchMirrorOptions) (*SearchMirror, error) {
	panic("search mirror: task 8 placeholder")
}

func (m *SearchMirror) RunOnce(ctx context.Context, progress SearchIndexProgressReporter) error {
	panic("search mirror: task 8 placeholder")
}

func (m *SearchMirror) ForceFullCopy(ctx context.Context, progress SearchIndexProgressReporter) error {
	panic("search mirror: task 8 placeholder")
}

func (m *SearchMirror) StoodDown() bool {
	panic("search mirror: task 8 placeholder")
}

func (m *SearchMirror) StartProcessLease(ctx context.Context) {
	panic("search mirror: task 8 placeholder")
}

// SetSearchMirrorRuntime records this process's mirror config and worker (nil when not running).
func SetSearchMirrorRuntime(cfg SearchMirrorConfig, m *SearchMirror) {
	panic("search mirror: task 8 placeholder")
}

// searchMirrorRuntime returns what SetSearchMirrorRuntime recorded; the zero config
// (Configured false) and nil when it was never called. Read by Tasks 12 and 13.
func searchMirrorRuntime() (SearchMirrorConfig, *SearchMirror) {
	panic("search mirror: task 8 placeholder")
}
