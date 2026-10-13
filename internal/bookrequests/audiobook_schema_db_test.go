package bookrequests

import (
	"errors"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/testutil/migrationfixture"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const audiobookAcquisitionMigration = "bloem_audiobook_request_acquisition"

// audiobookAcquisitionVersions returns the acquisition migration version and
// the version immediately below it, so the rollback test never depends on a
// hard-coded neighbor.
func audiobookAcquisitionVersions(t *testing.T) (int64, int64) {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, "sql")
	if err != nil {
		t.Fatal(err)
	}
	versions := []int64{}
	var target int64
	for _, entry := range entries {
		name := entry.Name()
		prefix, rest, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			continue
		}
		versions = append(versions, version)
		if strings.TrimSuffix(rest, ".sql") == audiobookAcquisitionMigration {
			target = version
		}
	}
	if target == 0 {
		t.Fatalf("migration %s not found", audiobookAcquisitionMigration)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	var previous int64
	for _, version := range versions {
		if version < target {
			previous = version
		}
	}
	return target, previous
}

func requireCheckViolation(t *testing.T, pool *pgxpool.Pool, label, statement string, args ...any) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	_, err = tx.Exec(t.Context(), statement, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("%s: expected a check violation, got %v", label, err)
	}
}

func TestAudiobookAcquisitionSchemaDatabase(t *testing.T) {
	_, v, pool := databaseFixture(t)
	for _, name := range []string{"bloem_book_requests_fulfilment_check", "bloem_ebook_request_dispatches_provider_status_check"} {
		var exists bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1)`, name).Scan(&exists); err != nil || !exists {
			t.Fatalf("constraint %s missing: %v", name, err)
		}
	}
	for _, table := range []string{"bloem_audiobook_request_integrations", "bloem_audiobook_request_acquisitions", "bloem_audiobook_request_decisions"} {
		var exists bool
		if err := pool.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	r := submit(t, NewService(pool, nil), v, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	for _, fulfilment := range []string{"downloading", "review"} {
		if _, err := pool.Exec(t.Context(), `UPDATE bloem_book_requests SET fulfilment=$2 WHERE id=$1`, r.ID, fulfilment); err != nil {
			t.Fatalf("fulfilment %s rejected: %v", fulfilment, err)
		}
	}
	requireCheckViolation(t, pool, "unknown fulfilment", `UPDATE bloem_book_requests SET fulfilment='imported' WHERE id=$1`, r.ID)
	if _, err := pool.Exec(t.Context(), `INSERT INTO bloem_ebook_request_dispatches(request_id,integration_id,submission_request_id,descriptor,provider_status) VALUES($1,'fixture',$1,'{}','review')`, r.ID); err != nil {
		t.Fatalf("review provider status rejected: %v", err)
	}
	requireCheckViolation(t, pool, "unknown provider status", `UPDATE bloem_ebook_request_dispatches SET provider_status='imported' WHERE request_id=$1`, r.ID)
	if _, err := pool.Exec(t.Context(), `INSERT INTO bloem_audiobook_request_acquisitions(request_id) VALUES($1)`, r.ID); err != nil {
		t.Fatalf("acquisition row rejected: %v", err)
	}
	requireCheckViolation(t, pool, "attempt range", `UPDATE bloem_audiobook_request_acquisitions SET attempt=0 WHERE request_id=$1`, r.ID)
	requireCheckViolation(t, pool, "review kind", `UPDATE bloem_audiobook_request_acquisitions SET review_kind='other' WHERE request_id=$1`, r.ID)
	requireCheckViolation(t, pool, "decision outcome", `INSERT INTO bloem_audiobook_request_decisions(request_id,attempt,phase,outcome,envelope) VALUES($1,1,'select','imported','{}')`, r.ID)
	requireCheckViolation(t, pool, "decision phase", `INSERT INTO bloem_audiobook_request_decisions(request_id,attempt,phase,outcome,envelope) VALUES($1,1,'other','held','{}')`, r.ID)
	if _, err := pool.Exec(t.Context(), `INSERT INTO bloem_audiobook_request_decisions(request_id,attempt,phase,outcome,envelope) VALUES($1,1,'select','held','{"v":1}')`, r.ID); err != nil {
		t.Fatalf("decision row rejected: %v", err)
	}
	// A binding cascades when Silo deletes the bound library, unlike the ebook
	// binding, so an audiobook binding never blocks a library delete.
	var deleteRule string
	if err := pool.QueryRow(t.Context(), `SELECT confdeltype::text FROM pg_constraint WHERE conrelid='bloem_audiobook_request_integrations'::regclass AND confrelid='media_folders'::regclass`).Scan(&deleteRule); err != nil || deleteRule != "c" {
		t.Fatalf("binding library FK should cascade: %q %v", deleteRule, err)
	}
	// Silo-switch tables stay untouched by this lane.
	var foreign int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_constraint WHERE conrelid IN ('users'::regclass,'user_profiles'::regclass,'auth_sessions'::regclass,'request_integrations'::regclass) AND conname LIKE '%audiobook%'`).Scan(&foreign); err != nil || foreign != 0 {
		t.Fatalf("audiobook constraints on Silo tables: %d %v", foreign, err)
	}
}

func TestAudiobookAcquisitionMigrationRollbackDatabase(t *testing.T) {
	baseline := migrationfixture.Before(t, migrations.BloemFS, migrationfixture.CompatibilityRemoval)
	_, v, pool := databaseFixtureWithMigrations(t, baseline)
	_, previous := audiobookAcquisitionVersions(t)
	r := submit(t, NewService(pool, nil), v, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	bookSQL(t, pool, `INSERT INTO bloem_audiobook_request_decisions(request_id,attempt,phase,outcome,envelope) VALUES($1,1,'select','no_match','{"v":1}')`, r.ID)
	if err := database.MigrateDownTo(t.Context(), pool, baseline, "sql", previous); err == nil {
		t.Fatal("rollback succeeded while acquisition history exists")
	}
	bookSQL(t, pool, `DELETE FROM bloem_audiobook_request_decisions`)
	if err := database.MigrateDownTo(t.Context(), pool, baseline, "sql", previous); err != nil {
		t.Fatalf("clean rollback: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(t.Context(), `SELECT to_regclass('bloem_audiobook_request_acquisitions') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("acquisition table survived rollback: %v %v", exists, err)
	}
	requireCheckViolation(t, pool, "restored fulfilment check", `UPDATE bloem_book_requests SET fulfilment='downloading' WHERE id=$1`, r.ID)
	if err := database.RunMigrations(t.Context(), pool, baseline, "sql"); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}
