package userdb

import (
	"path/filepath"
	"testing"
)

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
