package userdb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// Bloem shipped its compatibility cleanup as SQLite v32 before Silo used v32
// for user_devices.custom_name. A store Bloem left at 32 skipped Silo's v32
// and lost the Jellyfin display-preference table, so Bloem's steps restore both.
func TestBloemV32StoreGainsDeviceCustomNameAndDisplayPrefs(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE jellycompat_displayprefs;
DROP TABLE user_devices;
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
	var tables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'jellycompat_displayprefs'`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("jellycompat_displayprefs tables = %d, %v; want 1", tables, err)
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

// Bloem's former v34 dropped the Jellyfin display-preference table from a
// store that already had user_devices.custom_name; v35 brings the table back.
func TestBloemV34StoreRegainsDisplayPrefs(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE jellycompat_displayprefs; PRAGMA user_version = 34;`); err != nil {
		t.Fatalf("seed Bloem v34 store: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	if version, err := userVersion(db); err != nil || version != schemaVersion {
		t.Fatalf("user_version = %d, %v; want %d", version, err, schemaVersion)
	}
	var tables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'jellycompat_displayprefs'`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("jellycompat_displayprefs tables = %d, %v; want 1", tables, err)
	}
}
