package invitations

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type atomicInvitationFixture struct {
	schema          string
	repo            *Repository
	pool            *pgxpool.Pool
	applicationName string
	ctx             context.Context
}

func atomicInvitationDB(t *testing.T) atomicInvitationFixture {
	t.Helper()
	ctx := t.Context()
	// Membership, profile-entitlement and login-registry functions explicitly
	// reference public. A partial LIKE clone with a custom search_path can write
	// into the source database and omits its foreign keys and triggers. Migrate
	// a disposable database instead so every dependency remains real and local.
	pool := newInvitationOrganizationDatabase(t, ctx)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users(username,email,password_hash,role,enabled) VALUES('inviter','inviter@example.invalid','x','admin',true)`); err != nil {
		t.Fatal(err)
	}
	// Invitations are sent only after the server's initial owner has activated
	// the default organization.
	if _, err := tenancy.NewStore(pool).ProvisionDefaultMembership(ctx, 1, models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.NewStore(pool).ActivateInitialOwnership(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// Upstream policy-race tests create a settings-only shadow in this schema.
	// Both the shadow and public belong to this fixture's disposable database;
	// membership functions still target its fully migrated public schema.
	const schema = "invitation_policy"
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg := pool.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	isolated, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(isolated.Close)
	return atomicInvitationFixture{schema: schema, repo: NewRepository(isolated), pool: isolated, applicationName: cfg.ConnConfig.RuntimeParams["application_name"], ctx: ctx}
}

func (f atomicInvitationFixture) invite(t *testing.T, name string) *models.Invitation {
	t.Helper()
	inv, err := f.repo.Create(t.Context(), models.CreateInvitationInput{Email: name + "@example.invalid", Role: "user", CreateProfile: true, ShowTour: true, InvitedBy: 1, ExpiresAt: time.Now().Add(time.Hour)}, HashToken(name))
	if err != nil {
		t.Fatal(err)
	}
	return inv
}
func (f atomicInvitationFixture) provision(provider userstore.UserStoreProvider) func(*models.Invitation, pgx.Tx) (*models.User, error) {
	accounts := f.accounts(provider)
	return func(inv *models.Invitation, tx pgx.Tx) (*models.User, error) {
		created, err := accounts.CreateAccountWithMembershipInTransaction(f.ctx, tx, auth.CreateAccountInput{User: models.CreateUserInput{Username: inv.Email, Email: inv.Email, Password: "fixture-password", Role: inv.Role}, DefaultProfile: auth.DefaultProfileOptions{Enabled: inv.CreateProfile, Name: "Home"}})
		return created.User, err
	}
}
func (f atomicInvitationFixture) counts(t *testing.T, inv *models.Invitation, wantUsers, wantProfiles, wantClaims int) {
	t.Helper()
	var users, profiles, claims, memberships, loginEmails int
	if err := f.pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM users WHERE username=$1),
		(SELECT count(*) FROM user_profiles p JOIN users u ON u.id=p.user_id WHERE u.username=$1),
		(SELECT count(*) FROM invitations WHERE id=$2 AND accepted_at IS NOT NULL),
		(SELECT count(*) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE u.username=$1),
		(SELECT count(*) FROM login_email_registry WHERE normalized_email=$1)`, inv.Email, inv.ID).
		Scan(&users, &profiles, &claims, &memberships, &loginEmails); err != nil {
		t.Fatal(err)
	}
	if users != wantUsers || profiles != wantProfiles || claims != wantClaims || memberships != wantUsers || loginEmails != wantUsers {
		t.Fatalf("users/profiles/claims/memberships/loginEmails=%d/%d/%d/%d/%d want %d/%d/%d/%d/%d", users, profiles, claims, memberships, loginEmails, wantUsers, wantProfiles, wantClaims, wantUsers, wantUsers)
	}
}
func (f atomicInvitationFixture) waitBlocked(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		var blocked bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, f.applicationName).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		runtime.Gosched()
	}
}
func TestInvitationAtomicConcurrentAccept(t *testing.T) {
	f := atomicInvitationDB(t)
	inv := f.invite(t, "concurrent")
	provision := f.provision(pgstore.NewPostgresProvider(f.pool))
	var calls atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			<-start
			_, err := f.repo.Accept(t.Context(), inv.TokenHash, func(i *models.Invitation, tx pgx.Tx) (*models.User, error) { calls.Add(1); return provision(i, tx) })
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrNotClaimable) && !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if wins != 1 || calls.Load() != 1 {
		t.Fatalf("wins=%d provisions=%d", wins, calls.Load())
	}
	f.counts(t, inv, 1, 1, 1)
}
func TestInvitationAtomicRollbackAndExpiry(t *testing.T) {
	for _, scenario := range []string{"callback failure", "deferred commit failure", "expiry during provisioning", "expiry after lock wait"} {
		t.Run(scenario, func(t *testing.T) {
			f := atomicInvitationDB(t)
			inv := f.invite(t, "rollback")
			provision := f.provision(pgstore.NewPostgresProvider(f.pool))
			if scenario == "deferred commit failure" {
				_, err := f.pool.Exec(t.Context(), `CREATE FUNCTION fail_invitation_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture deferred failure'; END $$; CREATE CONSTRAINT TRIGGER fail_invitation_commit AFTER UPDATE ON invitations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.accepted_at IS NOT NULL) EXECUTE FUNCTION fail_invitation_commit()`)
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "expiry after lock wait" {
				tx, err := f.pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
				if _, err = tx.Exec(t.Context(), `UPDATE invitations SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, inv.ID); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				var calls atomic.Int32
				go func() {
					_, err := f.repo.Accept(t.Context(), inv.TokenHash, func(i *models.Invitation, tx pgx.Tx) (*models.User, error) { calls.Add(1); return provision(i, tx) })
					done <- err
				}()
				f.waitBlocked(t)
				if err = tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err = <-done; !errors.Is(err, ErrNotClaimable) && !errors.Is(err, ErrNotFound) {
					t.Fatalf("wait expiry=%v", err)
				}
				if calls.Load() != 0 {
					t.Fatal("expired invite provisioned")
				}
			} else {
				_, err := f.repo.Accept(t.Context(), inv.TokenHash, func(i *models.Invitation, tx pgx.Tx) (*models.User, error) {
					user, err := provision(i, tx)
					if err != nil {
						return nil, err
					}
					switch scenario {
					case "callback failure":
						return nil, errors.New("fixture failure")
					case "expiry during provisioning":
						_, err = tx.Exec(t.Context(), `UPDATE invitations SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, i.ID)
					}
					return user, err
				})
				if err == nil {
					t.Fatal("expected transaction failure")
				}
				switch scenario {
				case "callback failure":
					if err.Error() != "fixture failure" {
						t.Fatalf("did not reach callback failure: %v", err)
					}
				case "deferred commit failure":
					assertInvitationTriggerFailure(t, err, "fixture deferred failure")
				case "expiry during provisioning":
					if !errors.Is(err, ErrNotClaimable) {
						t.Fatal(err)
					}
				}
			}
			f.counts(t, inv, 0, 0, 0)
		})
	}
}
