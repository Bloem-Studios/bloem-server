package userdb

import (
	"context"
	"database/sql"
	"fmt"
)

// bloemSchemaVersion is the last SQLite step Bloem runs after Silo's v33.
const bloemSchemaVersion = 35

// migrateBloemSQLite runs Bloem's SQLite steps after Silo's v33.
//
// Bloem once shipped a compatibility cleanup as version 32, before Silo
// claimed 32 for user_devices.custom_name, and then as version 34. A store
// Bloem left at 32 skipped Silo's v32, so v34 adds that column. Both cleanups
// dropped the Jellyfin display-preference table, which Silo's restored
// Jellyfin-compatible server needs, so v35 recreates it for every store that
// passed through them; the preferences they deleted do not return.
func migrateBloemSQLite(tx *sql.Tx, version int) error {
	ctx := context.Background()
	if version < 34 && !columnExists(tx, "user_devices", "custom_name") {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE user_devices ADD COLUMN custom_name TEXT`); err != nil {
			return fmt.Errorf("adding user_devices.custom_name: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, jellycompatDisplayPrefsSchema); err != nil {
		return fmt.Errorf("creating jellycompat_displayprefs: %w", err)
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", bloemSchemaVersion))
	return err
}
