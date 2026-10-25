package userdb

import (
	"context"
	"database/sql"
	"fmt"
)

// bloemSQLiteResumeVersion returns the version Silo's steps resume from.
// Bloem numbered its own steps 34 and 35 before Silo claimed both, so a store
// at either has not run Silo's v34 or v35. Those steps are idempotent, so the
// store resumes after v33 and runs Silo's later steps and Bloem's repair.
func bloemSQLiteResumeVersion(version int) int {
	if version == 34 || version == 35 {
		return 33
	}
	return version
}

// migrateBloemSQLite repairs, inside Silo's migration transaction, stores that
// passed through Bloem's former cleanup steps. It takes no version of its own:
// the store ends at Silo's schemaVersion.
//
// Bloem once shipped a compatibility cleanup as version 32, before Silo
// claimed 32 for user_devices.custom_name, and then as version 34. A store
// Bloem left at 32 skipped Silo's v32, so this step adds that column. Both
// cleanups dropped the Jellyfin display-preference table, which Silo's
// restored Jellyfin-compatible server needs, so the step recreates it for every
// store that passed through them; the preferences they deleted do not return.
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
	return nil
}
