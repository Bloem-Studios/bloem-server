package tenancy_test

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
)

func TestMirroredSiloEbookPolicyObserversAcceptSettingsWrites(t *testing.T) {
	ctx, pool, _, _ := mirroredPool(t)
	epoch := func() int64 {
		t.Helper()
		var n int64
		if err := pool.QueryRow(ctx, `SELECT revision FROM bloem_ebook_request_policy_epoch WHERE id`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	initial := epoch()
	exec(`INSERT INTO server_settings(key,value) VALUES('unrelated.observer_fixture','one') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`)
	if epoch() != initial {
		t.Fatal("unrelated upstream setting advanced ebook epoch")
	}
	exec(`DELETE FROM server_settings WHERE key=$1`, config.EbookRequestsEnabledSettingKey)
	before := epoch()
	exec(`INSERT INTO server_settings(key,value) VALUES($1,'false')`, config.EbookRequestsEnabledSettingKey)
	if epoch() != before+1 {
		t.Fatal("ebook insert did not advance epoch")
	}
	before = epoch()
	exec(`UPDATE server_settings SET value='false' WHERE key=$1`, config.EbookRequestsEnabledSettingKey)
	if epoch() != before {
		t.Fatal("unchanged ebook value advanced epoch")
	}
	exec(`UPDATE server_settings SET value='true' WHERE key=$1`, config.EbookRequestsEnabledSettingKey)
	if epoch() != before+1 {
		t.Fatal("ebook toggle did not advance epoch")
	}
	before = epoch()
	exec(`DELETE FROM server_settings WHERE key=$1`, config.EbookRequestsEnabledSettingKey)
	if epoch() != before+1 {
		t.Fatal("ebook delete did not advance epoch")
	}
	exec(`UPDATE request_settings SET requests_enabled=false WHERE id`)
	before = epoch()
	exec(`UPDATE request_settings SET requests_enabled=true WHERE id`)
	if epoch() != before+1 {
		t.Fatal("global enable did not advance epoch")
	}
	before = epoch()
	exec(`UPDATE request_settings SET requests_enabled=true WHERE id`)
	if epoch() != before {
		t.Fatal("unchanged global policy advanced epoch")
	}
	exec(`UPDATE request_settings SET requests_enabled=false WHERE id`)
	if epoch() != before+1 {
		t.Fatal("global disable did not advance epoch")
	}
}
