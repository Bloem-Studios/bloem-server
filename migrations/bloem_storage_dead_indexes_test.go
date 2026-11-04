package migrations

import (
	"strings"
	"testing"
)

// Native storage writes every entry and scan directory again on each scan, so
// an index no query reads only costs writes. The sidecar and ingestion indexes
// lost their queries when library storage locations replaced native onboarding
// (f59aaafce), and bloem_storage_scan_directories_pending_idx copied
// bloem_storage_pending_directories exactly.
func TestBloemStorageUnreadIndexesAreGonePostgres(t *testing.T) {
	conn := migratedTestConn(t)
	present := func(name string) bool {
		t.Helper()
		var found bool
		if err := conn.QueryRow(t.Context(), "SELECT to_regclass('public.' || $1) IS NOT NULL", name).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	var kept []string
	for _, name := range []string{
		"bloem_storage_entries_sidecar_name_idx",
		"bloem_storage_entries_sidecar_suffix_idx",
		"bloem_storage_ingestion_entries_idx",
		"bloem_storage_scan_directories_pending_idx",
	} {
		if present(name) {
			kept = append(kept, name)
		}
	}
	if len(kept) > 0 {
		t.Fatalf("unread native storage indexes still exist: %s", strings.Join(kept, ", "))
	}
	// pendingDirectoryTx walks a run's pending directories through this one.
	if !present("bloem_storage_pending_directories") {
		t.Fatal("bloem_storage_pending_directories is gone; a run's next pending directory needs it")
	}
}
