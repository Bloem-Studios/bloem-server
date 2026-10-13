package userdb

import (
	"path/filepath"
	"testing"
)

func TestRemovedCompatibilityPersistenceSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.db")
	db, err := NewUserDB(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='jellycompat_displayprefs'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("fresh database retains compatibility table")
	}
}
