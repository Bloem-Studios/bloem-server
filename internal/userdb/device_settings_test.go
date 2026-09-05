package userdb

import (
	"database/sql"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

func TestDeviceSettingsTiesAndRollback(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := InitSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	store := NewSQLiteUserStore(db)
	for _, p := range []string{"a", "b"} {
		if err := store.CreateProfile(ctx, userstore.Profile{ID: p, Name: p}); err != nil {
			t.Fatal(err)
		}
		for _, d := range []string{"one", "two"} {
			if err := store.RegisterDevice(ctx, userstore.DeviceEntry{ProfileID: p, DeviceID: d}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`UPDATE user_devices SET last_seen_at='2026-01-02T03:04:05.123456Z'`); err != nil {
		t.Fatal(err)
	}
	opts := userstore.DevicePageOptions{Limit: 1}
	for _, want := range []string{"a/one", "a/two", "b/one", "b/two"} {
		page, err := store.ListDeviceSettingsPage(ctx, opts)
		if err != nil || len(page) != 1 || page[0].ProfileID+"/"+page[0].DeviceID != want {
			t.Fatalf("tie page: %+v %v want %s", page, err, want)
		}
		v := page[0]
		opts.After = &userstore.DevicePosition{LastSeenAt: v.LastSeenAt, ProfileID: v.ProfileID, DeviceID: v.DeviceID}
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_device_delete BEFORE DELETE ON user_devices BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RemoveDeviceSettings(ctx, "a", "one", true); err == nil {
		t.Fatal("expected failure")
	}
	exists, err := store.DeviceExists(ctx, "a", "one")
	if err != nil || !exists {
		t.Fatalf("rollback: %v %v", exists, err)
	}
}
