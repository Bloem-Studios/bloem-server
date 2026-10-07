package bloemtestdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CloneNativeOnboarding copies an approved private fixture without changing it.
// The caller must close all clone pools before cleanup. Only the UUID-owned
// clone may receive migrations after the caller registers cleanup, opens a
// verified pool and calls PrepareNativeOnboardingPool. prepare=true is refused.
func CloneNativeOnboarding(ctx context.Context, dsn string, prepare bool) (string, func(context.Context) error, error) {
	if prepare {
		return "", nil, fmt.Errorf("register clone cleanup before opening its pool; use CloneNativeOnboarding with prepare=false")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return "", nil, fmt.Errorf("invalid private fixture configuration")
	}
	template := cfg.ConnConfig.Database
	if !strings.HasPrefix(template, "bloem_storage_test_") {
		return "", nil, fmt.Errorf("unapproved private fixture")
	}
	adminCfg := cfg.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	adminCfg.BeforeAcquire = func(ctx context.Context, conn *pgx.Conn) bool {
		var actual string
		return conn.QueryRow(ctx, "SELECT current_database()").Scan(&actual) == nil && actual == "postgres"
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminCfg)
	if err != nil {
		return "", nil, fmt.Errorf("private clone administrator unavailable")
	}
	name := "bloem_storage_test_acore_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		admin.Close()
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			return "", nil, fmt.Errorf("create private clone failed (SQLSTATE %s)", pe.Code)
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			return "", nil, fmt.Errorf("private database endpoint refused connection")
		}
		return "", nil, fmt.Errorf("create private clone failed (transport class %T; cause %T)", err, errors.Unwrap(err))
	}
	cleanup := func(ctx context.Context) error {
		defer admin.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			return fmt.Errorf("drop private clone failed")
		}
		var exists bool
		if err := admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil || exists {
			return fmt.Errorf("private clone cleanup unverified")
		}
		return nil
	}
	cfg.ConnConfig.Database = name
	cloneDSN, err := nativeOnboardingCloneURL(dsn, name)
	if err != nil {
		_ = cleanup(context.Background())
		return "", nil, err
	}
	cloneConfig, err := pgxpool.ParseConfig(cloneDSN)
	if err != nil || cloneConfig.ConnConfig.Database != name {
		_ = cleanup(context.Background())
		return "", nil, fmt.Errorf("clone DSN identity unverified")
	}

	return cloneDSN, cleanup, nil
}
