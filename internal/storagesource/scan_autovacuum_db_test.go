package storagesource

import (
	"slices"
	"testing"
)

// The scan tables are analyzed after a fixed number of changes, not after a
// share of their size. A run's rows are new to the planner until the table is
// analyzed; on a location with hundreds of thousands of directories a
// size-relative threshold is not reached during a run, the planner keeps
// treating the run as empty, and a page can take minutes and lose its lease.
func TestScanTablesUseFixedAutovacuumThresholdsDB(t *testing.T) {
	pool := storageTestPool(t)
	want := []string{
		"autovacuum_analyze_scale_factor=0",
		"autovacuum_analyze_threshold=10000",
		"autovacuum_vacuum_scale_factor=0",
		"autovacuum_vacuum_threshold=50000",
	}
	for _, table := range []string{
		"bloem_storage_scan_directories",
		"bloem_storage_scan_directory_roots",
		"bloem_storage_scan_directory_entries",
		"bloem_storage_scan_cursors",
		"bloem_storage_location_edges",
		"bloem_storage_location_entries",
	} {
		var options []string
		if err := pool.QueryRow(t.Context(), `SELECT coalesce(reloptions, '{}') FROM pg_class WHERE oid=$1::regclass`, "public."+table).Scan(&options); err != nil {
			t.Fatal(err)
		}
		for _, option := range want {
			if !slices.Contains(options, option) {
				t.Errorf("%s reloptions = %v, missing %s", table, options, option)
			}
		}
	}
}
