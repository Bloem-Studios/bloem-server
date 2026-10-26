package auth_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The provider boundary must reject unsupported profiles before any account,
// membership or store write. In particular, opening SQLite cannot be rolled
// back by the caller's PostgreSQL transaction.
func TestBloemAccountTransactionProviderPreflight(t *testing.T) {
	pool := bloemAccountTransactionDatabase(t)
	ctx := t.Context()
	tenants := tenancy.NewStore(pool)
	owner, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{
		Username: "owner", Email: "owner@example.test", Password: "fixture-password", Role: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.ProvisionDefaultMembership(ctx, owner.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.ActivateInitialOwnership(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	organization, err := tenants.DefaultOrganization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foreignID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,slug,name,status,owner_account_id) VALUES ($1,$2,'Other','active',$3)`, foreignID, foreignID.String(), owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_groups (organization_id,name,is_default) VALUES ($1,'Default',true)`, foreignID); err != nil {
		t.Fatal(err)
	}

	for _, entrypoint := range []string{"default", "organization", "organization-default"} {
		for _, backend := range []string{"nil", "hidden-postgres", "sqlite", "postgres", "notifications", "provider-only"} {
			for _, enabled := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/profile=%t", entrypoint, backend, enabled), func(t *testing.T) {
					dir := t.TempDir()
					var provider userstore.UserStoreProvider
					var observed *bloemObservedAccountProvider
					if backend != "nil" {
						var inner userstore.UserStoreProvider = pgstore.NewPostgresProvider(pool)
						if backend == "sqlite" {
							inner = userdb.NewSQLiteProvider(userdb.NewUserDBPool(userdb.PoolConfig{DataDir: dir}))
							t.Cleanup(func() {
								if err := inner.Close(); err != nil {
									t.Error(err)
								}
							})
						}
						if backend == "notifications" {
							inner = notifications.WrapUserStoreProvider(inner, &notifications.System{})
						}
						observed = &bloemObservedAccountProvider{UserStoreProvider: inner, hideStoreWriter: backend == "provider-only"}
						provider = observed
						if backend == "postgres" || backend == "notifications" || backend == "provider-only" {
							provider = bloemTransactionalAccountProvider{observed, inner.(bloemProviderProfileWriter)}
						}
					}
					accounts := auth.NewAccountProvisioner(auth.NewUserRepository(pool), provider)
					tx, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = tx.Rollback(context.Background()) }()
					before := bloemAccountTransactionCounts(t, tx)
					var sequenceBefore int64
					if err := tx.QueryRow(ctx, `SELECT last_value FROM users_id_seq`).Scan(&sequenceBefore); err != nil {
						t.Fatal(err)
					}
					name := uuid.NewString()
					input := auth.CreateAccountInput{
						User:           models.CreateUserInput{Username: name, Email: name + "@example.test", Password: "fixture-password", Role: "user"},
						DefaultProfile: auth.DefaultProfileOptions{Enabled: enabled, Name: "Home"},
					}
					var created *models.User
					expectedOrganization := organization.ID
					if entrypoint == "default" {
						created, err = accounts.CreateAccountInTransaction(ctx, tx, input)
					} else {
						if entrypoint == "organization" {
							expectedOrganization = foreignID
						}
						created, err = accounts.CreateAccountForOrganizationInTransaction(ctx, tx, expectedOrganization, input)
					}
					unsupported := backend == "nil" || backend == "hidden-postgres" || backend == "sqlite"
					if enabled && unsupported {
						if !errors.Is(err, auth.ErrTransactionalProfileUnavailable) {
							t.Errorf("error = %v, want ErrTransactionalProfileUnavailable", err)
						}
						if created != nil {
							t.Errorf("rejected create returned an account: %+v", created)
						}
						if after := bloemAccountTransactionCounts(t, tx); after != before {
							t.Errorf("writes before rejection: before=%v after=%v", before, after)
						}
						var sequenceAfter int64
						if err := tx.QueryRow(ctx, `SELECT last_value FROM users_id_seq`).Scan(&sequenceAfter); err != nil {
							t.Fatal(err)
						}
						if sequenceAfter != sequenceBefore {
							t.Errorf("account insert attempted: sequence %d -> %d", sequenceBefore, sequenceAfter)
						}
						if observed != nil && observed.calls != 0 {
							t.Errorf("ForUser calls = %d, want 0", observed.calls)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						if created == nil || created.AccountIncarnationID == uuid.Nil {
							t.Fatalf("created account = %+v", created)
						}
						// The provider's transactional writer makes the profile;
						// no per-user store is opened.
						if observed != nil && observed.calls != 0 {
							t.Errorf("ForUser calls = %d, want 0", observed.calls)
						}
						wantProfiles := 0
						if enabled {
							wantProfiles = 1
						}
						if err := tx.Commit(ctx); err != nil {
							t.Fatal(err)
						}
						var totalMemberships int
						if err := pool.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE account_id=$1`, created.ID).Scan(&totalMemberships); err != nil {
							t.Fatal(err)
						}
						if totalMemberships != 1 {
							t.Fatalf("created account has %d memberships, want only the selected organization", totalMemberships)
						}
						var groupOrganization string
						if err := pool.QueryRow(ctx, `SELECT g.organization_id::text FROM organization_memberships m JOIN users mu ON mu.id = m.account_id JOIN access_groups g ON g.id = mu.access_group_id WHERE m.account_id=$1`, created.ID).Scan(&groupOrganization); err != nil {
							t.Fatal(err)
						}
						if groupOrganization != expectedOrganization.String() {
							t.Fatalf("default access group belongs to %s, not selected organization", groupOrganization)
						}
						var accountCount, membershipCount, profileCount int
						if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=$1`, created.ID).Scan(&accountCount); err != nil {
							t.Fatal(err)
						}
						if err := pool.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE account_id=$1 AND organization_id=$2`, created.ID, expectedOrganization).Scan(&membershipCount); err != nil {
							t.Fatal(err)
						}
						if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_profiles WHERE user_id=$1`, created.ID).Scan(&profileCount); err != nil {
							t.Fatal(err)
						}
						if accountCount != 1 || membershipCount != 1 || profileCount != wantProfiles {
							t.Fatalf("committed account/membership/profile counts = %d/%d/%d", accountCount, membershipCount, profileCount)
						}
						if enabled {
							var profileName, profileOrganization string
							if err := pool.QueryRow(ctx, `SELECT name,organization_id::text FROM user_profiles WHERE user_id=$1`, created.ID).Scan(&profileName, &profileOrganization); err != nil {
								t.Fatal(err)
							}
							if profileName != "Home" || profileOrganization != expectedOrganization.String() {
								t.Fatalf("profile name/organization = %q/%q", profileName, profileOrganization)
							}
						}
					}
					files, err := os.ReadDir(dir)
					if err != nil {
						t.Fatal(err)
					}
					if len(files) != 0 {
						t.Errorf("provider created %d filesystem entries", len(files))
					}
				})
			}
		}
	}
}

type bloemObservedAccountProvider struct {
	userstore.UserStoreProvider
	calls           int
	hideStoreWriter bool
}

func (p *bloemObservedAccountProvider) ForUser(ctx context.Context, id int) (userstore.UserStore, error) {
	p.calls++
	store, err := p.UserStoreProvider.ForUser(ctx, id)
	if err == nil && p.hideStoreWriter {
		return struct{ userstore.UserStore }{store}, nil
	}
	return store, err
}

type bloemProviderProfileWriter interface {
	CreateProfileInTransaction(context.Context, pgx.Tx, int, userstore.Profile) error
}
type bloemTransactionalAccountProvider struct {
	*bloemObservedAccountProvider
	bloemProviderProfileWriter
}

func bloemAccountTransactionCounts(t *testing.T, tx pgx.Tx) [4]int {
	t.Helper()
	var counts [4]int
	if err := tx.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM organization_memberships), (SELECT count(*) FROM user_profiles), (SELECT count(*) FROM login_email_registry)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3]); err != nil {
		t.Fatal(err)
	}
	return counts
}
func bloemAccountTransactionDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newBloemAccountDatabase(t)
	return pool
}

// newBloemAccountDatabase returns a disposable, migrated database still in the
// compatibility phase.
func newBloemAccountDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("SILO_TEST_DATABASE_URL is required for account transaction regressions")
	}
	ctx := t.Context()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := "bloem_account_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	cfg.ConnConfig.Database = name
	cfg.ConnConfig.RuntimeParams["search_path"] = "public"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	return pool
}
