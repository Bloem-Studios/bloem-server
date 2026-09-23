package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// TestBloemDeviceSettingsRollbackSurfacesInjectedFailure pins the failure Silo's
// TestDeviceSettingsRollback only checks is non-nil: RemoveDeviceSettings must
// fail on the injected registry-delete error itself (not on some earlier
// tenancy prerequisite), and the canonical settings must survive it.
func TestBloemDeviceSettingsRollbackSurfacesInjectedFailure(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var id int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,role) VALUES($1,'user') RETURNING id`, fmt.Sprintf("bloem-device-rollback-%d", time.Now().UnixNano())).Scan(&id); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) }()
	provisionTestMembership(t, pool, id)
	store := newStore(pool, id)
	if err := store.CreateProfile(ctx, userstore.Profile{ID: "p", Name: "P"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterDevice(ctx, userstore.DeviceEntry{ProfileID: "p", DeviceID: "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSettingValue(ctx, userstore.SettingIdentity{Key: "theme", Scope: settingscontract.ScopeProfileDevice, ProfileID: "p", DeviceID: "d"}, json.RawMessage(`"dark"`)); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("bloem_device_rollback_%d", id)
	sqlText := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.user_id=%d THEN RAISE EXCEPTION 'injected device deletion failure'; END IF; RETURN OLD; END $$; CREATE TRIGGER %s BEFORE DELETE ON user_devices FOR EACH ROW EXECUTE FUNCTION %s()`, name, id, name, name)
	if _, err := pool.Exec(ctx, sqlText); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER %s ON user_devices; DROP FUNCTION %s()`, name, name))
	}()
	if _, err := store.RemoveDeviceSettings(ctx, "p", "d", true); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "P0001" || pgErr.Message != "injected device deletion failure" {
			t.Fatalf("expected injected device deletion failure, got %v", err)
		}
	} else {
		t.Fatal("expected injected device deletion failure")
	}
	page, err := store.ListDeviceSettingsPage(ctx, userstore.DevicePageOptions{ProfileID: "p", Limit: 10})
	if err != nil || len(page) != 1 || page[0].ChangedCount != 1 {
		t.Fatalf("partial cleanup: %+v %v", page, err)
	}
}
