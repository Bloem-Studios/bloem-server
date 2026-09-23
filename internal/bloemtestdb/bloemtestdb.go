// Package bloemtestdb prepares a shared PostgreSQL database for the Go test
// suite: it migrates the schema, finalizes the membership policy authority the
// way a production operator does, declares the fixture session markers at the
// database level, and installs the Bloem fixture-compatibility triggers in
// sql/fixture_compat.sql.
//
// It is development tooling. Nothing in the server imports it, the SQL is not a
// migration, and Prepare refuses any database whose name does not mark it as a
// test database, so a production DSN cannot receive the shim by accident.
package bloemtestdb

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

//go:embed sql/fixture_compat.sql
var fixtureCompatSQL string

// DatabaseSettings are applied with ALTER DATABASE .. SET, so every session
// opened afterwards carries them. The two writer markers are what the
// packages' own TestMain helpers already declare; fixture_compat arms the
// triggers.
var DatabaseSettings = [][2]string{
	{"bloem.membership_policy_writer", "v1"},
	{"bloem.schema_capability_writer", "v1"},
	{"bloem.fixture_compat", "on"},
}

// ErrNotATestDatabase is returned for a database name that does not look like
// a disposable test database.
var ErrNotATestDatabase = errors.New("bloemtestdb: refusing a database whose name neither contains \"test\" nor ends in \"_ci\"")

// IsTestDatabaseName reports whether name marks a disposable test database:
// it contains "test" or ends in "_ci".
func IsTestDatabaseName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "test") || strings.HasSuffix(lower, "_ci")
}

// Options control Prepare.
type Options struct {
	// Recreate drops and recreates the database (through the "postgres"
	// maintenance database on the same server) before migrating.
	Recreate bool
	// Logf receives progress lines; nil discards them.
	Logf func(format string, args ...any)
}

// Prepare readies the database named by dsn. See the package comment.
func Prepare(ctx context.Context, dsn string, opts Options) error {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("bloemtestdb: parse dsn: %w", err)
	}
	name := cfg.ConnConfig.Database
	if !IsTestDatabaseName(name) {
		return fmt.Errorf("%w: %q", ErrNotATestDatabase, name)
	}
	if opts.Recreate {
		if err := recreate(ctx, cfg, name); err != nil {
			return err
		}
		logf("recreated database %s", name)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("bloemtestdb: connect: %w", err)
	}
	defer pool.Close()

	migCtx, cancel := database.MigrationContext(ctx)
	err = database.RunMigrations(migCtx, pool, migrations.FS, "sql")
	cancel()
	if err != nil {
		return fmt.Errorf("bloemtestdb: migrate: %w", err)
	}
	logf("migrations applied")

	changed, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool)
	if err != nil {
		return fmt.Errorf("bloemtestdb: finalize membership policy authority: %w", err)
	}
	logf("membership policy authority finalized (changed=%t)", changed)

	for _, setting := range DatabaseSettings {
		if _, err := pool.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s SET %s = %s",
			pgx.Identifier{name}.Sanitize(), setting[0], pgx.Identifier{setting[1]}.Sanitize())); err != nil {
			return fmt.Errorf("bloemtestdb: set %s: %w", setting[0], err)
		}
	}
	logf("database settings applied")

	if _, err := pool.Exec(ctx, fixtureCompatSQL); err != nil {
		return fmt.Errorf("bloemtestdb: install fixture compatibility triggers: %w", err)
	}
	logf("fixture compatibility triggers installed")
	return nil
}

func recreate(ctx context.Context, cfg *pgxpool.Config, name string) error {
	admin := cfg.ConnConfig.Copy()
	admin.Database = "postgres"
	conn, err := pgx.ConnectConfig(ctx, admin)
	if err != nil {
		return fmt.Errorf("bloemtestdb: connect to maintenance database: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("bloemtestdb: drop %s: %w", name, err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		return fmt.Errorf("bloemtestdb: create %s: %w", name, err)
	}
	return nil
}
