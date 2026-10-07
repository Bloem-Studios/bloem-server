//go:build integration

package nativestorage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeLineageSQL(t *testing.T, down bool) string {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "sql/*_bloem_native_storage_source_lineage.sql")
	if err != nil || len(names) != 1 {
		t.Fatal("exactly one owned lineage migration required")
	}
	raw, err := migrations.FS.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("Goose lineage Up/Down missing")
	}
	if down {
		return parts[1]
	}
	return strings.TrimPrefix(parts[0], "-- +goose Up")
}
func nativeLineageExec(t *testing.T, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func nativeLineageBegin(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

// Full logical public rows (including Goose history) and installed semantic
// object definitions are hashed without logging private data or incidental OIDs.
func nativeLineageSnapshot(t *testing.T, tx pgx.Tx) string {
	t.Helper()
	rows, err := tx.Query(t.Context(), "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	for _, table := range tables {
		var data string
		err = tx.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(j ORDER BY j::text)::text,'[]') FROM (SELECT to_jsonb(t) j FROM public."+pgx.Identifier{table}.Sanitize()+" t) x").Scan(&data)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(digest, "%s:%s\n", table, data)
	}
	for _, sql := range []string{
		`SELECT c.relname||':'||a.attname||':'||format_type(a.atttypid,a.atttypmod)||':'||a.attnotnull||':'||COALESCE(pg_get_expr(d.adbin,d.adrelid),'') FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE n.nspname='public' AND a.attnum>0 AND NOT a.attisdropped`,
		`SELECT c.relname||':'||k.conname||':'||pg_get_constraintdef(k.oid)||':'||k.convalidated FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'`,
		`SELECT c.relname||':'||t.tgname||':'||pg_get_triggerdef(t.oid)||':'||t.tgenabled::text FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND NOT t.tgisinternal`,
		`SELECT pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.prokind IN ('f','p')`,
		`SELECT indexdef FROM pg_indexes WHERE schemaname='public'`,
	} {
		rows, err = tx.Query(t.Context(), sql)
		if err != nil {
			t.Fatal(err)
		}
		var objects []string
		for rows.Next() {
			var object string
			if err = rows.Scan(&object); err != nil {
				t.Fatal(err)
			}
			objects = append(objects, object)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(objects)
		for _, object := range objects {
			fmt.Fprintln(digest, object)
		}
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func TestNativeOnboardingSourceLineageMigrationDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	svc, source, _ := nativeDomainSourceFixture(t, pool, actor)
	owner := nativeTestSourceOwner(t, svc, source.SourceKey)
	repo := storagesource.NewRepository(pool)
	detached, err := repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, PluginID: source.PluginID, ProviderSourceID: "legacy-detached", RootEntryID: "detached-root", ConfigurationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, InstallationID: source.InstallationID, PluginID: source.PluginID, ProviderSourceID: "legacy-unmarked", RootEntryID: "legacy-root", ConfigurationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	up, down := nativeLineageSQL(t, false), nativeLineageSQL(t, true)
	t.Run("known_attached_marked_and_unknown_detached", func(t *testing.T) {
		tx := nativeLineageBegin(t, pool)
		nativeLineageExec(t, tx, "ALTER TABLE public.bloem_storage_sources DROP COLUMN latest_installation_id")
		// Move just the legacy test source to a real nonmarked installation.
		var id int64
		err = tx.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,enabled,update_policy,owner_id) VALUES($1,'1','/test-only/legacy',false,'manual',$2) RETURNING id`, source.PluginID, owner).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		nativeLineageExec(t, tx, "UPDATE bloem_storage_sources SET installation_id=$2 WHERE key=$1", legacy.Key, id)
		nativeLineageExec(t, tx, up)
		if lineage := nativeLineage(t, tx, source.SourceKey); lineage == nil || *lineage != *source.InstallationID {
			t.Fatal("known attached marked backfill missing")
		}
		for _, key := range []uuid.UUID{detached.Key, legacy.Key} {
			if nativeLineage(t, tx, key) != nil {
				t.Fatal("ambiguous detached/unmarked lineage guessed")
			}
		}
		if !catalog.NativeStorageSchemaReady(t.Context(), tx) {
			t.Fatal("backfilled exact schema readiness false")
		}
		if _, err = tx.Exec(t.Context(), "UPDATE bloem_storage_sources SET latest_installation_id=0 WHERE key=$1", detached.Key); err == nil {
			t.Fatal("positive bigint witness constraint admitted zero")
		}
	})
	t.Run("invalid_attached_marker_atomic_refusal_beside_valid_sibling", func(t *testing.T) {
		tx := nativeLineageBegin(t, pool)
		nativeLineageExec(t, tx, "ALTER TABLE public.bloem_storage_sources DROP COLUMN latest_installation_id")
		nativeLineageExec(t, tx, "UPDATE bloem_storage_sources SET plugin_id='mismatched-plugin' WHERE key=$1", legacy.Key)
		before := nativeLineageSnapshot(t, tx)
		nativeLineageExec(t, tx, "SAVEPOINT lineage_failure")
		if _, err = tx.Exec(t.Context(), up); err == nil {
			t.Fatal("invalid attached marked association was backfilled")
		}
		nativeLineageExec(t, tx, "ROLLBACK TO SAVEPOINT lineage_failure")
		if after := nativeLineageSnapshot(t, tx); after != before {
			t.Fatal("failed Up altered semantic objects / full logical rows / Goose history")
		}
		t.Log("failed Up exact full logical row/object/Goose snapshot unchanged", before)
	})
	t.Run("down_refuses_every_retained_source", func(t *testing.T) {
		tx := nativeLineageBegin(t, pool)
		before := nativeLineageSnapshot(t, tx)
		nativeLineageExec(t, tx, "SAVEPOINT lineage_down")
		if _, err = tx.Exec(t.Context(), down); err == nil {
			t.Fatal("Down erased retained source lineage")
		}
		nativeLineageExec(t, tx, "ROLLBACK TO SAVEPOINT lineage_down")
		if before != nativeLineageSnapshot(t, tx) {
			t.Fatal("refused Down changed full rows/objects/Goose")
		}
	})
	t.Run("down_refuses_unknown_detached_only", func(t *testing.T) {
		tx := nativeLineageBegin(t, pool)
		nativeLineageExec(t, tx, "UPDATE bloem_storage_sources SET installation_id=NULL,latest_installation_id=NULL,enabled=false")
		before := nativeLineageSnapshot(t, tx)
		nativeLineageExec(t, tx, "SAVEPOINT unknown_down")
		if _, err = tx.Exec(t.Context(), down); err == nil {
			t.Fatal("Down accepted unknown detached retained rows")
		}
		nativeLineageExec(t, tx, "ROLLBACK TO SAVEPOINT unknown_down")
		if before != nativeLineageSnapshot(t, tx) {
			t.Fatal("unknown Down changed rows/objects/Goose")
		}
	})
}

func TestNativeOnboardingSourceLineageReadinessDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	if !catalog.NativeStorageSchemaReady(t.Context(), pool) {
		t.Fatal("exact fresh full schema is not ready")
	}
	for _, test := range []struct{ name, sql string }{
		{"missing_column", "ALTER TABLE public.bloem_storage_sources DROP COLUMN latest_installation_id"},
		{"wrong_int32", "ALTER TABLE public.bloem_storage_sources ALTER COLUMN latest_installation_id TYPE integer"},
		{"not_nullable", "ALTER TABLE public.bloem_storage_sources ALTER COLUMN latest_installation_id SET NOT NULL"},
		{"auto_default", "ALTER TABLE public.bloem_storage_sources ALTER COLUMN latest_installation_id SET DEFAULT 1"},
		{"missing_positive_check", "ALTER TABLE public.bloem_storage_sources DROP CONSTRAINT bloem_storage_sources_latest_installation_positive"},
		{"relaxed_check", "ALTER TABLE public.bloem_storage_sources DROP CONSTRAINT bloem_storage_sources_latest_installation_positive; ALTER TABLE public.bloem_storage_sources ADD CONSTRAINT bloem_storage_sources_latest_installation_positive CHECK(latest_installation_id IS NULL OR latest_installation_id>=0)"},
		{"unvalidated_check", "ALTER TABLE public.bloem_storage_sources DROP CONSTRAINT bloem_storage_sources_latest_installation_positive; ALTER TABLE public.bloem_storage_sources ADD CONSTRAINT bloem_storage_sources_latest_installation_positive CHECK(latest_installation_id IS NULL OR latest_installation_id>0) NOT VALID"},
		{"foreign_key", "ALTER TABLE public.bloem_storage_sources ADD CONSTRAINT forbidden_lineage_fk FOREIGN KEY(latest_installation_id) REFERENCES plugin_installations(id)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := nativeLineageBegin(t, pool)
			nativeLineageExec(t, tx, test.sql)
			if catalog.NativeStorageSchemaReady(t.Context(), tx) {
				t.Error("readiness admits lineage metadata/constraint drift")
			}
			_ = tx.Rollback(t.Context())
			if !catalog.NativeStorageSchemaReady(t.Context(), pool) {
				t.Fatal("DDL rollback did not restore readiness")
			}
		})
	}
	tx := nativeLineageBegin(t, pool)
	nativeLineageExec(t, tx, nativeLineageSQL(t, true))
	if catalog.NativeStorageSchemaReady(t.Context(), tx) {
		t.Error("empty-source Down did not remove readiness")
	}
	nativeLineageExec(t, tx, nativeLineageSQL(t, false))
	if !catalog.NativeStorageSchemaReady(t.Context(), tx) {
		t.Fatal("empty-source Up recovery did not restore exact readiness")
	}
}
