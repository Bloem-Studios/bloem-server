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

	for _, entrypoint := range []string{"default", "organization"} {
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
					memberships := &bloemAccountMemberships{store: tenants}
					accounts := auth.NewAccountProvisioner(auth.NewUserRepository(pool), provider)
					accounts.SetMembershipProvisioner(memberships)
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
					var created auth.CreatedAccount
					expectedOrganization := organization.ID
					if entrypoint == "default" {
						created, err = accounts.CreateAccountInTransaction(ctx, tx, input)
					} else {
						expectedOrganization = foreignID
						created, err = accounts.CreateAccountForOrganizationInTransaction(ctx, tx, foreignID, input)
					}
					unsupported := backend == "nil" || backend == "hidden-postgres" || backend == "sqlite"
					if enabled && unsupported {
						if !errors.Is(err, auth.ErrTransactionalProfileUnavailable) {
							t.Errorf("error = %v, want ErrTransactionalProfileUnavailable", err)
						}
						if created != (auth.CreatedAccount{}) {
							t.Errorf("rejected create returned identities: %+v", created)
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
						if memberships.calls != 0 {
							t.Errorf("membership calls = %d, want 0", memberships.calls)
						}
						if observed != nil && observed.calls != 0 {
							t.Errorf("ForUser calls = %d, want 0", observed.calls)
						}
					} else if enabled && backend == "provider-only" {
						// A whole-provider advertisement never replaces validation of the
						// actual store that will perform the transactional profile write.
						if err == nil || !strings.Contains(err.Error(), "user store does not support transactional profile creation") {
							t.Fatalf("store capability validation = %v", err)
						}
						if observed.calls != 1 || memberships.calls != 1 {
							t.Fatalf("store validation did not follow provisioning: store=%d memberships=%d", observed.calls, memberships.calls)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						if created.User == nil || created.User.AccountIncarnationID == uuid.Nil || created.OrganizationID != expectedOrganization || created.MembershipID == uuid.Nil {
							t.Fatalf("created identities = %+v", created)
						}
						wantStoreCalls := 0
						if enabled {
							wantStoreCalls = 1
						}
						if observed != nil && observed.calls != wantStoreCalls {
							t.Errorf("ForUser calls = %d, want %d", observed.calls, wantStoreCalls)
						}
						if memberships.calls != 1 {
							t.Errorf("membership calls = %d, want 1", memberships.calls)
						}
						if err := tx.Commit(ctx); err != nil {
							t.Fatal(err)
						}
						var accountCount, membershipCount, profileCount int
						if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=$1`, created.User.ID).Scan(&accountCount); err != nil {
							t.Fatal(err)
						}
						if err := pool.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE id=$1 AND account_id=$2 AND organization_id=$3`, created.MembershipID, created.User.ID, expectedOrganization).Scan(&membershipCount); err != nil {
							t.Fatal(err)
						}
						if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_profiles WHERE user_id=$1`, created.User.ID).Scan(&profileCount); err != nil {
							t.Fatal(err)
						}
						if accountCount != 1 || membershipCount != 1 || profileCount != wantStoreCalls {
							t.Fatalf("committed account/membership/profile counts = %d/%d/%d", accountCount, membershipCount, profileCount)
						}
						if enabled {
							var profileName, profileOrganization string
							if err := pool.QueryRow(ctx, `SELECT name,organization_id::text FROM user_profiles WHERE id=$1 AND user_id=$2`, created.ProfileID, created.User.ID).Scan(&profileName, &profileOrganization); err != nil {
								t.Fatal(err)
							}
							if profileName != "Home" || profileOrganization != expectedOrganization.String() {
								t.Fatalf("profile name/organization = %q/%q", profileName, profileOrganization)
							}
						} else if created.ProfileID != "" {
							t.Fatalf("profile-disabled result contains profile %q", created.ProfileID)
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
type bloemAccountMemberships struct {
	store *tenancy.Store
	calls int
}

func (p *bloemAccountMemberships) ProvisionDefaultMembership(ctx context.Context, id int, role string) error {
	_, err := p.store.ProvisionDefaultMembership(ctx, id, role)
	return err
}
func (p *bloemAccountMemberships) ProvisionDefaultMembershipInTransaction(ctx context.Context, tx pgx.Tx, id int, role string) (uuid.UUID, uuid.UUID, error) {
	p.calls++
	m, err := p.store.ProvisionDefaultMembershipInTransaction(ctx, tx, id, role)
	return m.OrganizationID, m.ID, err
}
func (p *bloemAccountMemberships) ProvisionMembershipInTransaction(ctx context.Context, tx pgx.Tx, organization uuid.UUID, id int, role string) (uuid.UUID, uuid.UUID, error) {
	p.calls++
	m, err := p.store.ProvisionMembershipInTransaction(ctx, tx, organization, id, role)
	return m.OrganizationID, m.ID, err
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
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
