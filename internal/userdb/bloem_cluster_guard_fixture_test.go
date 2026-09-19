package userdb_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A real account satisfies the durable session FK. Keep the migrated schema,
// account policy authority and SQLite ownership rows inside one disposable DB.
func bloemClusterGuardSessionDatabase(t *testing.T) (*pgxpool.Pool, *models.User) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("SILO_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse maintenance database config: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = "public"
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	name := "bloem_cluster_guard_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database: %v", err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(dropCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database: %v", err)
		}
		admin.Close()
	})
	testConfig := config.Copy()
	testConfig.ConnConfig.Database = name
	pool, err = pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		t.Fatalf("connect disposable database: %v", err)
	}
	var isScratchDatabase bool
	if err := pool.QueryRow(ctx, `SELECT current_database() = $1`, name).Scan(&isScratchDatabase); err != nil {
		t.Fatalf("verify disposable database identity: %v", err)
	}
	if !isScratchDatabase {
		t.Fatal("disposable database identity mismatch")
	}
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}
	account, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{
		Username: "reconciler-session-account",
		Email:    "reconciler-session-account@example.test",
		Password: "fixture-reconciler-account-password",
		Role:     "user",
	})
	if err != nil {
		t.Fatalf("create session account: %v", err)
	}
	return pool, account
}
