package backup

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/blobstore"
)

// bloemUserDataBackups runs a test with Bloem's user-data backups on, as
// Bloem's server binary runs.
func bloemUserDataBackups(t *testing.T) {
	t.Helper()
	SetBloemUserDataOnly(true)
	t.Cleanup(func() { SetBloemUserDataOnly(false) })
}

// TestBloemUserDataBackupLeavesOutTheCatalogDB: a user-data backup's dump
// keeps the accounts, profiles, per-user tables, organizations, memberships
// and groups, leaves the catalog and the rest of the server's data out, and
// cannot replace a live database; a whole-server backup can.
func TestBloemUserDataBackupLeavesOutTheCatalogDB(t *testing.T) {
	bloemUserDataBackups(t)
	f := newSelectiveFixture(t)
	result, err := backupUnderLock(t, f.svc, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimPrefix(blobstore.Location(f.store), blobstore.BackendLocal+"|")
	src := openTestSource(t, filepath.Join(root, "backups", f.serverID, result.ID))
	excluded := src.Manifest.ExcludedTableData
	for _, table := range []string{"media_items", "media_files", "episodes", "media_item_embeddings", "server_settings"} {
		if !slices.Contains(excluded, table) {
			t.Errorf("a user-data backup keeps the data of %s", table)
		}
	}
	for _, table := range []string{"users", "user_profiles", "user_watch_history", "user_watch_progress", "organizations",
		"organization_memberships", "access_groups"} {
		if slices.Contains(excluded, table) {
			t.Errorf("a user-data backup leaves out the data of %s", table)
		}
	}
	// Sign-in sessions stay left out, as in every backup.
	if !slices.Contains(excluded, "auth_sessions") {
		t.Error("a user-data backup keeps sign-in sessions")
	}
	if err := bloemRefuseWholeServerRestore(src.Manifest); err == nil {
		t.Error("a user-data backup may replace the live database")
	}
	if err := bloemRefuseWholeServerRestore(&Manifest{ExcludedTableData: excludedTableData(false)}); err != nil {
		t.Errorf("a whole-server backup is refused: %v", err)
	}
}
