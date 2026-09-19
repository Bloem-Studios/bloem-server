package database

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBloemXtreamSourceMigration(t *testing.T) {
	const version int64 = 20260919133349
	const previous int64 = 20260919095615
	dsn := requiredPostgresTestDatabaseURL(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	host, name := cfg.ConnConfig.Host, cfg.ConnConfig.Database
	if (host != "127.0.0.1" && host != "localhost" && host != "::1") || (!strings.Contains(name, "test") && !strings.Contains(name, "_ci") && !strings.HasPrefix(name, "bloem_close_")) {
		t.Fatal("Xtream migration tests require a named, loopback-only disposable database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)
	provider, err := newMigrationProvider(pool, migrations.FS, "sql")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = provider.Close() }()
	if _, err := provider.UpTo(ctx, previous); err != nil {
		t.Fatal(err)
	}
	pool.Reset()
	if _, err := pool.Exec(ctx, `INSERT INTO livetv_tuners(id,type,device_id,base_url) VALUES('retained-hdhr','hdhomerun','fixture-hdhr','http://192.168.1.23')`); err != nil {
		t.Fatal(err)
	}
	up := func() {
		t.Helper()
		if _, err := provider.UpTo(ctx, version); err != nil {
			t.Fatal(err)
		}
		pool.Reset()
	}
	down := func() {
		t.Helper()
		if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", previous); err != nil {
			t.Fatal(err)
		}
		pool.Reset()
	}
	assertTables := func(want bool) {
		t.Helper()
		var credentials, leases, history bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('bloem_livetv_xtream_credentials') IS NOT NULL,to_regclass('bloem_livetv_xtream_leases') IS NOT NULL,coalesce((SELECT is_applied FROM goose_db_version WHERE version_id=$1 ORDER BY id DESC LIMIT 1),false)`, version).Scan(&credentials, &leases, &history); err != nil {
			t.Fatal(err)
		}
		if credentials != want || leases != want || history != want {
			t.Fatal("Xtream schema and migration history disagree")
		}
	}
	up()
	assertTables(true)
	down()
	assertTables(false)
	up()
	assertTables(true)
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_livetv_xtream_credentials(tuner_id,credentials) VALUES('retained-hdhr','fixture-plaintext')`); err == nil {
		t.Fatal("credential table accepted plaintext")
	}
	for _, test := range []struct{ name, setup, count, cleanup string }{
		{"provider", `INSERT INTO livetv_tuners(id,type,device_id) VALUES('fixture-xtream','xtream','fixture-xtream')`, `SELECT count(*) FROM livetv_tuners WHERE id='fixture-xtream'`, `DELETE FROM livetv_tuners WHERE id='fixture-xtream'`},
		{"credential", `INSERT INTO bloem_livetv_xtream_credentials(tuner_id,credentials) VALUES('retained-hdhr','enc:v1:fixture-envelope')`, `SELECT count(*) FROM bloem_livetv_xtream_credentials`, `DELETE FROM bloem_livetv_xtream_credentials`},
		{"expired lease", `INSERT INTO bloem_livetv_xtream_leases(lease_id,tuner_id,expires_at) VALUES('82b281cb-33c4-4f94-aaaf-a3f7c68c4990','retained-hdhr',clock_timestamp()-interval '1 hour')`, `SELECT count(*) FROM bloem_livetv_xtream_leases`, `DELETE FROM bloem_livetv_xtream_leases`},
		{"orphan guide", `INSERT INTO livetv_guide_sources(id,type,config_json) VALUES('fixture-guide','xtream','{"tuner_id":"removed-provider"}')`, `SELECT count(*) FROM livetv_guide_sources WHERE id='fixture-guide'`, `DELETE FROM livetv_guide_sources WHERE id='fixture-guide'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, test.setup); err != nil {
				t.Fatal(err)
			}
			err := MigrateDownTo(ctx, pool, migrations.FS, "sql", previous)
			if err == nil || !strings.Contains(err.Error(), "remove Xtream providers and stop their streams") {
				t.Fatalf("Down failed to refuse Xtream state: %v", err)
			}
			pool.Reset()
			assertTables(true)
			var count int
			if err := pool.QueryRow(ctx, test.count).Scan(&count); err != nil || count != 1 {
				t.Fatal("refused Down changed stored state")
			}
			if _, err := pool.Exec(ctx, test.cleanup); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := pool.Exec(ctx, `INSERT INTO livetv_tuners(id,type,device_id) VALUES('cascade-xtream','xtream','cascade-xtream'); INSERT INTO bloem_livetv_xtream_credentials(tuner_id,credentials) VALUES('cascade-xtream','enc:v1:fixture-envelope'); INSERT INTO bloem_livetv_xtream_leases(lease_id,tuner_id,expires_at) VALUES('ad7d5929-496e-40c5-a7f3-8f4f1a65db4e','cascade-xtream',clock_timestamp()); DELETE FROM livetv_tuners WHERE id='cascade-xtream'`); err != nil {
		t.Fatal(err)
	}
	var credentials, leases int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM bloem_livetv_xtream_credentials),(SELECT count(*) FROM bloem_livetv_xtream_leases)`).Scan(&credentials, &leases); err != nil || credentials != 0 || leases != 0 {
		t.Fatal("provider deletion retained credential or lease rows")
	}
	down()
	assertTables(false)
	up()
	assertTables(true)
	var retained int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM livetv_tuners WHERE id='retained-hdhr' AND type='hdhomerun'`).Scan(&retained); err != nil || retained != 1 {
		t.Fatal("Xtream migration changed existing HDHomeRun state")
	}
}
