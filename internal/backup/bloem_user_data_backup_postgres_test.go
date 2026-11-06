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
// and groups, leaves the catalog and the rest of the server's data out; its
// restore set leaves the plugins and settings out, it copies no server
// files, and it cannot replace a live database; a whole-server backup can.
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
	// The restore set holds the accounts and the sections their restore
	// maps by, not the plugins or settings, and no server files are copied:
	// avatars travel with the accounts.
	if src.Manifest.RestoreSet == nil {
		t.Fatal("a user-data backup has no restore set")
	}
	for section := range src.Manifest.RestoreSet.Sections {
		if !slices.Contains(bloemRestoreSetSections, section) {
			t.Errorf("a user-data backup's restore set holds section %s", section)
		}
	}
	for _, section := range []string{SectionPlugins, SectionSettings} {
		if _, ok := src.Manifest.RestoreSet.Sections[section]; ok {
			t.Errorf("a user-data backup's restore set holds the server's %s", section)
		}
	}
	for _, o := range src.Manifest.Objects {
		if strings.HasPrefix(o.Name, BlobsName) && o.Size > 4096 {
			t.Errorf("a user-data backup copies server files: %s is %d bytes", o.Name, o.Size)
		}
	}
	if err := bloemRefuseWholeServerRestore(src.Manifest); err == nil {
		t.Error("a user-data backup may replace the live database")
	}
	if err := bloemRefuseWholeServerRestore(&Manifest{ExcludedTableData: excludedTableData(false)}); err != nil {
		t.Errorf("a whole-server backup is refused: %v", err)
	}
}
