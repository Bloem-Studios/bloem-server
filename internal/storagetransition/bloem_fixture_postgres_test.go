package storagetransition

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Upstream PostgreSQL tests select an existing actor. Own that fixture here so
// a schema-only package database exercises them without borrowing another
// package's account or changing the shared schema template.
func TestMain(m *testing.M) {
	if os.Getenv("SILO_TEST_DATABASE_URL") == "" {
		os.Exit(m.Run())
	}
	cleanup, err := bloemStorageTransitionActor()
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare storage-transition actor: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "clean up storage-transition actor: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func bloemStorageTransitionActor() (func() error, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		return nil, fmt.Errorf("parse test database configuration: %T", err)
	}
	if !bloemtestdb.IsTestDatabaseName(cfg.ConnConfig.Database) || strings.Contains(strings.ToLower(cfg.ConnConfig.Database), "template") {
		return nil, fmt.Errorf("actor fixture requires a disposable test database, not a schema template")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open test database: %T", err)
	}
	var databaseName string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		pool.Close()
		return nil, fmt.Errorf("verify test database: %T", err)
	}
	if databaseName != cfg.ConnConfig.Database {
		pool.Close()
		return nil, fmt.Errorf("actor fixture connected to a different database")
	}
	identity := "storage-transition-fixture-" + uuid.NewString()
	actor, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{
		Username: identity, Email: identity + "@example.test", Password: uuid.NewString(), Role: models.RoleAdmin,
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create package actor: %T", err)
	}
	return func() error {
		defer pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, actor.ID); err != nil {
			return fmt.Errorf("delete package actor: %T", err)
		}
		return nil
	}, nil
}

func TestBloemStorageTransitionFixtureRefusesNonDisposableDatabase(t *testing.T) {
	for _, name := range []string{"production", "bloem_storage_test_template_owned"} {
		t.Run(name, func(t *testing.T) {
			// No connection is allowed: this fixture must reject both a live
			// database name and the gate's schema-template naming convention.
			t.Setenv("SILO_TEST_DATABASE_URL", "dbname="+name)
			cleanup, err := bloemStorageTransitionActor()
			if err == nil || cleanup != nil || !strings.Contains(err.Error(), "disposable test database") {
				t.Fatalf("fixture did not refuse database before connecting: cleanup=%v err=%v", cleanup != nil, err)
			}
		})
	}
}
