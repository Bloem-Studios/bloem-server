package bloemtestdb

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// RunIsolatedSuite gives one test process its own prepared database. Migration
// rollback and query-planner fixtures cannot change other packages' schemas or
// statistics. Missing optional database configuration retains ordinary skips.
func RunIsolatedSuite(run func() int) int {
	parentDSN := os.Getenv("SILO_TEST_DATABASE_URL")
	if parentDSN == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			fmt.Fprintln(os.Stderr, "SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
			return 1
		}
		return run()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	dsn, cleanup, err := isolateSuite(ctx, parentDSN)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolate test database: %v\n", err)
		return 1
	}
	code := 1
	if err := os.Setenv("SILO_TEST_DATABASE_URL", dsn); err != nil {
		fmt.Fprintf(os.Stderr, "set isolated test database: %v\n", err)
	} else {
		code = run()
	}
	if err := os.Setenv("SILO_TEST_DATABASE_URL", parentDSN); err != nil {
		fmt.Fprintf(os.Stderr, "restore test database environment: %v\n", err)
		code = 1
	}
	if err := cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "clean up isolated test database: %v\n", err)
		code = 1
	}
	return code
}

func isolateSuite(ctx context.Context, parentDSN string) (string, func() error, error) {
	cfg, err := pgx.ParseConfig(parentDSN)
	if err != nil {
		return "", nil, fmt.Errorf("parse parent dsn: %w", err)
	}
	// Refuse before opening even the maintenance connection.
	if !IsTestDatabaseName(cfg.Database) {
		return "", nil, ErrNotATestDatabase
	}
	name := "bloem_suite_test_" + strings.ToLower(rand.Text())
	dsn, err := suiteDSN(parentDSN, name)
	if err != nil {
		return "", nil, err
	}
	admin := cfg.Copy()
	admin.Database = "postgres"
	conn, err := pgx.ConnectConfig(ctx, admin)
	if err != nil {
		return "", nil, fmt.Errorf("connect to maintenance database: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		return "", nil, fmt.Errorf("create suite database: %w", err)
	}
	cleanup := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.ConnectConfig(ctx, admin)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close(ctx) }()
		_, err = conn.Exec(ctx, "DROP DATABASE "+ident+" WITH (FORCE)")
		return err
	}
	if err := Prepare(ctx, dsn, Options{}); err != nil {
		if cleanupErr := cleanup(); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup: %w", cleanupErr))
		}
		return "", nil, err
	}
	return dsn, cleanup, nil
}

func suiteDSN(parentDSN, name string) (string, error) {
	if strings.HasPrefix(parentDSN, "postgres://") || strings.HasPrefix(parentDSN, "postgresql://") {
		parsed, err := url.Parse(parentDSN)
		if err != nil {
			return "", err
		}
		parsed.Path = "/" + name
		parsed.RawPath = ""
		query := parsed.Query()
		query.Del("dbname")
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}
	// The generated name has no quoting characters; a final dbname overrides
	// earlier keyword values while retaining connection and TLS settings.
	return parentDSN + " dbname=" + name, nil
}
