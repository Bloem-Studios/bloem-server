package userdb

import (
	"database/sql"
	"fmt"
)

type execSQL interface {
	Exec(string, ...any) (sql.Result, error)
}

// removeCompatibilitySQLite drops the retired Jellyfin display-preference
// storage and its settings. It is idempotent.
func removeCompatibilitySQLite(db execSQL) error {
	if _, err := db.Exec("DROP TABLE IF EXISTS jellycompat_displayprefs"); err != nil {
		return err
	}
	_, err := db.Exec("DELETE FROM user_settings WHERE key LIKE 'jellycompat:%'")
	return err
}

// migrateBloemV34 is Bloem's SQLite step after Silo's v33. Bloem shipped its
// compatibility cleanup as v32 before Silo claimed v32 for
// user_devices.custom_name, so a store Bloem left at 32 skipped Silo's v32
// and lacks that column; add it here.
func migrateBloemV34(tx *sql.Tx) error {
	if err := removeCompatibilitySQLite(tx); err != nil {
		return err
	}
	if !columnExists(tx, "user_devices", "custom_name") {
		if _, err := tx.Exec(`ALTER TABLE user_devices ADD COLUMN custom_name TEXT`); err != nil {
			return fmt.Errorf("adding user_devices.custom_name: %w", err)
		}
	}
	_, err := tx.Exec("PRAGMA user_version = 34")
	return err
}
