package userdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// Bloem shipped its compatibility cleanup as SQLite v32 before Silo used v32
// for user_devices.custom_name. A store Bloem left at 32 skips Silo's v32, so
// Bloem's v34 must add the column.
func TestBloemV32StoreGainsDeviceCustomName(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE user_devices;
CREATE TABLE user_devices (
    profile_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    device_name TEXT NOT NULL DEFAULT '',
    device_platform TEXT NOT NULL DEFAULT '',
    last_seen_at TEXT NOT NULL,
    PRIMARY KEY (profile_id, device_id)
);
INSERT INTO user_devices (profile_id, device_id, device_name, last_seen_at) VALUES ('p1', 'tv', 'Apple TV', '2026-10-01T00:00:00Z');
PRAGMA user_version = 32;`); err != nil {
		t.Fatalf("seed Bloem v32 store: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	if version, err := userVersion(db); err != nil || version != schemaVersion {
		t.Fatalf("user_version = %d, %v; want %d", version, err, schemaVersion)
	}
	store := NewSQLiteUserStore(db)
	if err := store.RenameDevice(context.Background(), "p1", "tv", "Living Room"); err != nil {
		t.Fatalf("RenameDevice after migration: %v", err)
	}
	page, err := store.ListDeviceSettingsPage(context.Background(), userstore.DevicePageOptions{Limit: 10})
	if err != nil || len(page) != 1 || page[0].CustomName != "Living Room" {
		t.Fatalf("migrated device = %+v, %v", page, err)
	}
}

func TestCompatibilityCleanupSQLiteUpgradePreservesNativeSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.db")
	store, err := NewUserDB(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	db := store.DB
	if _, err := db.Exec(jellycompatDisplayPrefsSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO jellycompat_displayprefs(prefs_id,client,value,updated_at) VALUES('retired','emby','{}',0);
 INSERT INTO user_settings(key,value) VALUES('jellycompat:displayprefs:retired:emby','{}'),('ui_theme','native-theme');
 PRAGMA user_version=31`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store, err = NewUserDB(path, 1)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err := store.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='jellycompat_displayprefs'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("upgrade recreated retired table")
		}
		if err := store.DB.QueryRow(`SELECT count(*) FROM user_settings WHERE key LIKE 'jellycompat:%'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("retired settings remain")
		}
		theme, err := GetSetting(store.DB, "ui_theme")
		if err != nil || theme != "native-theme" {
			t.Fatalf("native theme=%q err=%v", theme, err)
		}
		version, err := userVersion(store.DB)
		if err != nil || version != schemaVersion {
			t.Fatalf("version=%d err=%v", version, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
