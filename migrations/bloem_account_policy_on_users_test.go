package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// beforeAccountPolicyOnUsers is the newest migration that still keeps the
// account policy on the memberships.
const beforeAccountPolicyOnUsers = 20261024034851

var accountPolicyColumns = []string{
	"access_group_id", "permissions", "library_ids", "max_playback_quality",
	"max_streams", "max_transcodes", "transcode_allowed", "audio_transcode_allowed",
	"download_allowed", "download_transcode_allowed", "requests_allowed",
	"max_profiles", "access_policy_revision",
}

// accountPolicyDB migrates a disposable database to just before the policy
// moves back onto users, with one account in the default organization.
func accountPolicyDB(t *testing.T) (*sql.DB, *goose.Provider) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("test database required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = "postgres"
	admin := stdlib.OpenDB(*cfg)
	name := fmt.Sprintf("account_policy_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	cfg.Database = name
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, mustSub(t, "sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), beforeAccountPolicyOnUsers); err != nil {
		t.Fatal(err)
	}
	exec(t, db,
		`INSERT INTO users (id, username, role) VALUES (91001, 'policy-account', 'user')`,
		`INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		 VALUES (bloem_default_organization_id(), 91001, 'active', 'user')`,
	)
	return db, provider
}

func exec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", strings.Fields(statement)[0], err)
		}
	}
}

// execTx runs statements in one transaction, as the phase changes require
// their transaction-local markers.
func execTx(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range statements {
		if _, err := tx.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", strings.Fields(statement)[0], err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func switchingEnabled(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var enabled bool
	if err := db.QueryRowContext(t.Context(), `SELECT bloem_silo_switching_enabled()`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	return enabled
}

func assertPolicyOnlyOnUsers(t *testing.T, db *sql.DB) {
	t.Helper()
	var leftover int
	if err := db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'organization_memberships'
		  AND column_name = ANY($1)`, accountPolicyColumns).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("organization_memberships still has %d policy columns", leftover)
	}
	var onUsers int
	if err := db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'users' AND column_name = ANY($1)`, accountPolicyColumns).Scan(&onUsers); err != nil {
		t.Fatal(err)
	}
	if onUsers != len(accountPolicyColumns) {
		t.Fatalf("users has %d of the %d policy columns", onUsers, len(accountPolicyColumns))
	}
	var authority *string
	if err := db.QueryRowContext(t.Context(), `SELECT to_regclass('public.membership_policy_authority')::text`).Scan(&authority); err != nil {
		t.Fatal(err)
	}
	if authority != nil {
		t.Fatal("membership_policy_authority survived")
	}
}

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code + " " + pgErr.Message
	}
	return ""
}

// A finalized database held the policy on the memberships and only a renamed
// rollback projection on users. The membership's policy is what survives.
func TestAccountPolicyMovesBackFromFinalizedMembershipsPostgres(t *testing.T) {
	db, provider := accountPolicyDB(t)
	finalize := []string{
		`SELECT set_config('bloem.membership_policy_finalizer', 'v1', true)`,
		`DROP TRIGGER IF EXISTS users_membership_policy_authority_fence ON public.users`,
	}
	for _, column := range accountPolicyColumns {
		finalize = append(finalize, fmt.Sprintf(`ALTER TABLE public.users RENAME COLUMN %s TO rollback_membership_%s`, column, column))
	}
	finalize = append(finalize, `UPDATE public.membership_policy_authority SET phase = 'finalized', finalized_at = now() WHERE singleton`)
	execTx(t, db, finalize...)
	execTx(t, db,
		`SELECT set_config('bloem.membership_policy_writer', 'v1', true)`,
		`UPDATE organization_memberships SET max_streams = 3, requests_allowed = false,
		        permissions = '{marker_edit,download}', access_policy_revision = 7
		 WHERE account_id = 91001`,
	)

	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	assertPolicyOnlyOnUsers(t, db)
	var streams int
	var requests bool
	var permissions string
	var revision int64
	if err := db.QueryRowContext(t.Context(), `
		SELECT max_streams, requests_allowed, array_to_string(permissions, ','), access_policy_revision
		FROM users WHERE id = 91001`).Scan(&streams, &requests, &permissions, &revision); err != nil {
		t.Fatal(err)
	}
	if streams != 3 || requests || permissions != "marker_edit,download" || revision != 7 {
		t.Fatalf("account policy = streams %d, requests %v, permissions %q, revision %d", streams, requests, permissions, revision)
	}
	if switchingEnabled(t, db) {
		t.Fatal("a finalized database became switchable")
	}
}

// A mirrored database stays switchable: the policy Silo wrote stays on users,
// a second organization is still refused, and an account Silo creates still
// gets its default-organization membership at commit.
func TestAccountPolicyKeepsSiloSwitchingFromMirroredPostgres(t *testing.T) {
	db, provider := accountPolicyDB(t)
	execTx(t, db,
		`SELECT set_config('bloem.membership_policy_mirror_enabler', 'v1', true)`,
		`UPDATE public.membership_policy_authority SET phase = 'mirrored', mirrored_at = now() WHERE singleton`,
	)
	exec(t, db, `UPDATE users SET max_streams = 2 WHERE id = 91001`)

	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	assertPolicyOnlyOnUsers(t, db)
	if !switchingEnabled(t, db) {
		t.Fatal("a mirrored database lost Silo switching")
	}
	var streams int
	if err := db.QueryRowContext(t.Context(), `SELECT max_streams FROM users WHERE id = 91001`).Scan(&streams); err != nil {
		t.Fatal(err)
	}
	if streams != 2 {
		t.Fatalf("max_streams = %d, want 2", streams)
	}
	_, err := db.ExecContext(t.Context(), `INSERT INTO organizations (name, slug, status) VALUES ('Second', 'second', 'initializing')`)
	if !strings.Contains(sqlState(err), "bloem_silo_switching_single_organization") {
		t.Fatalf("second organization while switchable: %v", err)
	}
	exec(t, db,
		`INSERT INTO users (id, username, role) VALUES (91002, 'silo-created', 'user')`,
		`INSERT INTO user_profiles (id, user_id, name, is_primary) VALUES ('silo-profile', 91002, 'Silo', true)`,
	)
	var organization string
	if err := db.QueryRowContext(t.Context(), `
		SELECT m.organization_id::text FROM organization_memberships m
		JOIN user_profiles p ON p.user_id = m.account_id AND p.organization_id = m.organization_id
		WHERE m.account_id = 91002 AND m.organization_id = bloem_default_organization_id()`).Scan(&organization); err != nil {
		t.Fatalf("Silo-shaped account and profile were not placed: %v", err)
	}
}

// A fresh database ends with switching off: policy writes go straight to
// users, organizations can be added, and an account has one membership.
func TestAccountPolicyOnUsersForFreshDatabasePostgres(t *testing.T) {
	db, provider := accountPolicyDB(t)
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	assertPolicyOnlyOnUsers(t, db)
	if switchingEnabled(t, db) {
		t.Fatal("a fresh database became switchable")
	}
	exec(t, db,
		`UPDATE users SET max_transcodes = 1 WHERE id = 91001`,
		`INSERT INTO organizations (id, name, slug, status) VALUES ('0c8d2a9e-6d5f-4a36-9a48-1d8f8e64c0a1', 'Second', 'second', 'initializing')`,
	)
	_, err := db.ExecContext(t.Context(), `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ('0c8d2a9e-6d5f-4a36-9a48-1d8f8e64c0a1', 91001, 'active', 'user')`)
	if !strings.HasPrefix(sqlState(err), "23505") {
		t.Fatalf("second membership for one account: %v", err)
	}
}

// An account's access group must belong to the account's organization, as the
// membership's organization-scoped foreign key required before.
func TestAccountGroupMustBelongToTheAccountOrganizationPostgres(t *testing.T) {
	db, provider := accountPolicyDB(t)
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	exec(t, db,
		`INSERT INTO organizations (id, name, slug, status) VALUES ('5b0f0f5e-2b8e-4c63-8c1f-0e0d4c3f7a10', 'Other', 'other', 'initializing')`,
		`INSERT INTO access_groups (id, name, organization_id) OVERRIDING SYSTEM VALUE VALUES (92001, 'Other group', '5b0f0f5e-2b8e-4c63-8c1f-0e0d4c3f7a10')`,
		`INSERT INTO access_groups (id, name, organization_id) OVERRIDING SYSTEM VALUE VALUES (92002, 'Home group', bloem_default_organization_id())`,
		`UPDATE users SET access_group_id = 92002 WHERE id = 91001`,
	)
	_, err := db.ExecContext(t.Context(), `UPDATE users SET access_group_id = 92001 WHERE id = 91001`)
	if !strings.Contains(sqlState(err), "another organization") {
		t.Fatalf("foreign group accepted: %v", err)
	}
	_, err = db.ExecContext(t.Context(), `
		UPDATE organization_memberships SET organization_id = '5b0f0f5e-2b8e-4c63-8c1f-0e0d4c3f7a10' WHERE account_id = 91001`)
	if !strings.Contains(sqlState(err), "another organization") {
		t.Fatalf("membership moved away from its account's group: %v", err)
	}
}
