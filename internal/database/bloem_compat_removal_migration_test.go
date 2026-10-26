package database

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Silo-Server/silo-server/migrations"
)

// Bloem-owned compatibility tables go; the Jellyfin and Audiobookshelf tables
// belong to upstream Silo's compatibility servers, which Bloem runs as well.
func TestBloemCompatibilityTablesRemovedOnFreshInstall(t *testing.T) {
	pool := newDisposableMigrationDatabase(t)
	runAllMigrations(t, pool)
	exists := func(table string) bool {
		t.Helper()
		var exists bool
		if err := pool.QueryRow(t.Context(), "SELECT to_regclass('public.' || $1) IS NOT NULL", table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}
	for _, table := range []string{"compat_applications", "compat_application_enrollments", "compat_application_credentials", "compat_application_audit", "bloem_livetv_compat_streams"} {
		if exists(table) {
			t.Errorf("removed compatibility table remains: %s", table)
		}
	}
	for _, table := range []string{"abs_audiobook_author_counts", "abs_sessions", "abs_bookmarks", "abs_playback_sessions", "abs_rss_feeds", "jellycompat_sessions", "jellycompat_playback_sessions", "jellycompat_device_profiles", "jellycompat_displayprefs"} {
		if !exists(table) {
			t.Errorf("upstream Silo table removed: %s", table)
		}
	}
}

// compatibilityBaselineFS preserves the historical migration chain for tests
// of the old data-copy migrations and the pre-removal upgrade boundary. The
// forward-only migrations are left out, because rollback tests cross every
// later migration's Down.
func compatibilityBaselineFS(t *testing.T) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	err := fs.WalkDir(migrations.FS, "sql", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, "_remove_bloem_compatibility.sql") ||
			strings.HasSuffix(path, "_remove_pastime_managed_tracking.sql") ||
			strings.HasSuffix(path, "_bloem_account_policy_on_users.sql") ||
			strings.HasSuffix(path, "_bloem_remove_music.sql") ||
			strings.HasSuffix(path, "_bloem_remove_lifecycle_receipts.sql") {
			return nil
		}
		data, err := migrations.FS.ReadFile(path)
		if err != nil {
			return err
		}
		out[path] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBloemCompatibilityRemovalPreservesNativeDataOnUpgrade(t *testing.T) {
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(t.Context(), pool, compatibilityBaselineFS(t), "sql"); err != nil {
		t.Fatal(err)
	}
	var userID int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role) VALUES('native-compat-removal','fixture@example.test','fixture','user') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_items(content_id,type,title) VALUES('compat-removal-native-book','audiobook','Native audiobook');
 INSERT INTO server_settings(key,value) VALUES('audiobooks.abs.fixture','invented-fixture') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value;
 INSERT INTO server_settings(key,value) VALUES('jellyfin_compat.server_id','invented-silo-server-id') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value;`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO user_personal_collections(id,user_id,profile_id,name) VALUES('native-list',$1,'','Native list')`,
		`INSERT INTO user_personal_collection_items(user_id,collection_id,media_item_id) VALUES($1,'native-list','compat-removal-native-book')`,
		`INSERT INTO abs_sessions(user_id,token_hash,device_id) VALUES($1,'invented-retired-hash','invented-device')`,
		`INSERT INTO user_watch_progress(user_id,profile_id,media_item_id,position_seconds,duration_seconds) VALUES($1,'','compat-removal-native-book',42,120)`,
	} {
		if _, err := pool.Exec(t.Context(), statement, userID); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunMigrations(t.Context(), pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var title, name, item string
	if err := pool.QueryRow(t.Context(), `SELECT m.title,c.name,i.media_item_id FROM media_items m JOIN user_personal_collection_items i ON i.media_item_id=m.content_id JOIN user_personal_collections c ON c.user_id=i.user_id AND c.id=i.collection_id WHERE c.user_id=$1 AND c.id='native-list'`, userID).Scan(&title, &name, &item); err != nil {
		t.Fatal(err)
	}
	if title != "Native audiobook" || name != "Native list" || item != "compat-removal-native-book" {
		t.Fatalf("native data changed: %q %q %q", title, name, item)
	}
	var position, duration float64
	if err := pool.QueryRow(t.Context(), `SELECT position_seconds,duration_seconds FROM user_watch_progress WHERE user_id=$1 AND media_item_id='compat-removal-native-book'`, userID).Scan(&position, &duration); err != nil {
		t.Fatal(err)
	}
	if position != 42 || duration != 120 {
		t.Fatalf("native progress changed: %v/%v", position, duration)
	}
	// Silo's own state survives for a switch back: its server identity, its
	// Audiobookshelf settings and sessions.
	var serverID, absSetting string
	var absSessions int
	if err := pool.QueryRow(t.Context(), `SELECT
		(SELECT value FROM server_settings WHERE key='jellyfin_compat.server_id'),
		(SELECT value FROM server_settings WHERE key='audiobooks.abs.fixture'),
		(SELECT count(*) FROM abs_sessions WHERE user_id=$1)`, userID).Scan(&serverID, &absSetting, &absSessions); err != nil {
		t.Fatal(err)
	}
	if serverID != "invented-silo-server-id" || absSetting != "invented-fixture" || absSessions != 1 {
		t.Fatalf("upstream Silo state changed: server id %q, setting %q, sessions %d", serverID, absSetting, absSessions)
	}
	// A subsequent startup must not recreate tables or fail historical hooks.
	if err := RunMigrations(t.Context(), pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
}

func TestBloemCompatibilityRemovalFailsAtomicallyOnUnknownDependency(t *testing.T) {
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(t.Context(), pool, compatibilityBaselineFS(t), "sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE VIEW native_unknown_compat_dependency AS SELECT * FROM compat_application_audit`); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(t.Context(), pool, migrations.FS, "sql"); err == nil {
		t.Fatal("unknown dependency was silently deleted")
	}
	for _, table := range []string{"compat_applications", "compat_application_enrollments", "compat_application_credentials", "compat_application_audit"} {
		var exists bool
		if err := pool.QueryRow(t.Context(), "SELECT to_regclass('public.' || $1) IS NOT NULL", table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("failed migration partially removed %s", table)
		}
	}
}
