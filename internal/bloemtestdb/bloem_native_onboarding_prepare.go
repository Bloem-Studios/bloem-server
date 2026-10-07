package bloemtestdb

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NativeOnboardingPoolConfig requires the helper's exact UUID namespace and
// verifies each actual connection before it can be acquired by a writer.
// A PostgreSQL connection cannot change its database during a transaction.
func NativeOnboardingPoolConfig(dsn string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !nativeOnboardingCloneName(cfg.ConnConfig.Database) {
		return nil, fmt.Errorf("unowned private clone configuration")
	}
	expected := cfg.ConnConfig.Database
	cfg.BeforeAcquire = func(ctx context.Context, conn *pgx.Conn) bool {
		var actual string
		return conn.QueryRow(ctx, "SELECT current_database()").Scan(&actual) == nil && actual == expected
	}
	return cfg, nil
}

// PrepareNativeOnboardingPool resets and fully prepares only the already-open
// caller pool. Register cleanup before opening it. Unlike the general Prepare
// entrypoint, this uses the same verified pool for reset, default embedded
// migrations, authority finalization, database settings and fixture triggers.
func PrepareNativeOnboardingPool(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil || !nativeOnboardingCloneName(pool.Config().ConnConfig.Database) || pool.Config().BeforeAcquire == nil {
		return fmt.Errorf("verified private clone pool required")
	}
	expected := pool.Config().ConnConfig.Database
	verify := func() error {
		var actual string
		if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&actual); err != nil || actual != expected {
			return fmt.Errorf("private clone caller identity unverified")
		}
		return nil
	}
	if err := verify(); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		return fmt.Errorf("reset private clone failed")
	}
	if err := verify(); err != nil {
		return err
	}
	migCtx, cancel := database.MigrationContext(ctx)
	err := database.RunMigrations(migCtx, pool, migrations.FS, "sql")
	cancel()
	if err != nil {
		return fmt.Errorf("prepare private clone migrations: %w", err)
	}
	if err := verify(); err != nil {
		return err
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		return fmt.Errorf("prepare private clone authority: %w", err)
	}
	for _, setting := range DatabaseSettings {
		if err := verify(); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s SET %s = %s",
			pgx.Identifier{expected}.Sanitize(), setting[0], pgx.Identifier{setting[1]}.Sanitize())); err != nil {
			return fmt.Errorf("prepare private clone settings failed")
		}
	}
	// Settings above apply to newly opened sessions, including the fixture's
	// membership-policy writer markers. No caller transactions are open yet.
	pool.Reset()
	if err := verify(); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, fixtureCompatSQL); err != nil {
		return fmt.Errorf("prepare private clone fixture triggers: %w", err)
	}
	return nil
}
