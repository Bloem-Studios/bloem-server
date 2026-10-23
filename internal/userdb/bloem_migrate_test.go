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
// store that already had user_devices.custom_name; Bloem's step brings the
// table back.
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

// A store Bloem left at its former v35 never ran Silo's v34 or v35, which
// reused those numbers, so it resumes after v33 and gets both.
func TestBloemV35StoreRunsSiloV34AndV35(t *testing.T) {
	db := newSingleConnSchemaDB(t)
	if _, err := db.Exec(`
ALTER TABLE series_playback_preferences DROP COLUMN file_id;
ALTER TABLE series_playback_preferences DROP COLUMN edition_key;
INSERT INTO profile_section_overrides (id, profile_id, scope, library_id, section_id, hidden, section_type, is_user_added, user_section_type, created_at, updated_at) VALUES
    ('added', 'p1', 'home', NULL, NULL, 0, NULL, 1, 'next_up', 'now', 'now'),
    ('gems', 'p1', 'home', NULL, NULL, 0, NULL, 1, 'hidden_gems', 'now', 'now');
PRAGMA user_version = 35;`); err != nil {
		t.Fatalf("seed Bloem v35 store: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	if version, err := userVersion(db); err != nil || version != schemaVersion {
		t.Fatalf("user_version = %d, %v; want %d", version, err, schemaVersion)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, column := range []string{"file_id", "edition_key"} {
		if !columnExists(tx, "series_playback_preferences", column) {
			t.Fatalf("series_playback_preferences.%s missing after migration", column)
		}
	}
	var nextUp int
	if err := tx.QueryRow(`SELECT count(*) FROM profile_section_overrides WHERE user_section_type = 'next_up'`).Scan(&nextUp); err != nil || nextUp != 0 {
		t.Fatalf("added Next Up rows = %d, %v; want 0", nextUp, err)
	}
}
