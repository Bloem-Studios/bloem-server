package auth_test

// Bloem's own account writes while Silo switching is enabled: they must
// succeed, within the guards, and land on the users policy columns upstream
// Silo reads.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// bloemSwitchableDatabase holds one account Silo created, then enables switching.
func bloemSwitchableDatabase(t *testing.T) (context.Context, *pgxpool.Pool, int) {
	t.Helper()
	ctx := t.Context()
	pool := newBloemAccountDatabase(t)
	var account int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled, max_streams, transcode_allowed, access_group_id)
		VALUES ('silo-u1', 'silo-u1@example.invalid', 'x', 'user', true, 2, true, (SELECT id FROM access_groups WHERE is_default))
		RETURNING id`).Scan(&account); err != nil {
		t.Fatalf("silo account: %v", err)
	}
	if _, err := tenancy.EnableSiloSwitching(ctx, pool); err != nil {
		t.Fatalf("enable silo switching: %v", err)
	}
	return ctx, pool, account
}

func TestBloemAccountPolicyUpdateWhenSwitchable(t *testing.T) {
	ctx, pool, account := bloemSwitchableDatabase(t)
	users := auth.NewUserRepository(pool)
	if err := users.Update(ctx, account, models.UpdateUserInput{
		MaxStreams:       models.SetValue(6),
		TranscodeAllowed: models.SetValue(false),
	}); err != nil {
		t.Fatalf("bloem policy update while switchable: %v", err)
	}
	var streams int
	var transcode bool
	if err := pool.QueryRow(ctx, `SELECT max_streams, transcode_allowed FROM users WHERE id = $1`, account).Scan(&streams, &transcode); err != nil {
		t.Fatal(err)
	}
	if streams != 6 || transcode {
		t.Fatalf("users max_streams=%d transcode_allowed=%t, want 6 and false", streams, transcode)
	}
}

func TestBloemAccountCreateWhenSwitchable(t *testing.T) {
	ctx, pool, _ := bloemSwitchableDatabase(t)
	users := auth.NewUserRepository(pool)
	accounts := auth.NewAccountProvisioner(users, pgstore.NewPostgresProvider(pool))
	maxStreams := 4
	created, err := accounts.CreateAccount(ctx, auth.CreateAccountInput{
		User: models.CreateUserInput{
			Username: "bloem-u2", Email: "bloem-u2@example.invalid", Password: "fixture-password",
			Role: models.RoleUser, MaxStreams: &maxStreams,
		},
		DefaultProfile: auth.DefaultProfileOptions{Enabled: true, Name: "Main"},
	})
	if err != nil {
		t.Fatalf("bloem create account while switchable: %v", err)
	}
	var memberships, streams int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM organization_memberships
		        WHERE account_id = $1 AND organization_id = public.bloem_default_organization_id()),
		       (SELECT max_streams FROM users WHERE id = $1)`, created.ID,
	).Scan(&memberships, &streams); err != nil {
		t.Fatal(err)
	}
	if memberships != 1 || streams != 4 {
		t.Fatalf("default memberships=%d max_streams=%d, want 1 and 4", memberships, streams)
	}
}

// A household while Silo switching is on, as Bloem keeps it: a login is
// made and linked, its household gets a shared pool, a profile moves between
// the accounts, and suspending the main account switches the login off and
// back. Every write must pass the switching triggers and land where stock
// Silo reads it (users.enabled).
func TestBloemHouseholdWhenSwitchable(t *testing.T) {
	ctx, pool, main := bloemSwitchableDatabase(t)
	users := auth.NewUserRepository(pool)
	accounts := auth.NewAccountProvisioner(users, pgstore.NewPostgresProvider(pool))
	maxProfiles := 2
	login, err := accounts.CreateAccount(ctx, auth.CreateAccountInput{
		User: models.CreateUserInput{
			Username: "bloem-login", Email: "bloem-login@example.invalid", Password: "fixture-password",
			Role: models.RoleUser, MaxProfiles: &maxProfiles,
		},
		DefaultProfile: auth.DefaultProfileOptions{Enabled: true, Name: "Login"},
	})
	if err != nil {
		t.Fatalf("bloem create login account while switchable: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET household_main_user_id = $1 WHERE id = $2`, main, login.ID); err != nil {
		t.Fatalf("linking a household login while switchable: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_household_pools (main_user_id, max_streams, max_transcodes) VALUES ($1, 3, 2)`, main); err != nil {
		t.Fatalf("a household pool while switchable: %v", err)
	}

	mainStore, err := pgstore.NewPostgresProvider(pool).ForUser(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Main", "Sam"} {
		if err := mainStore.CreateProfile(ctx, userstore.Profile{ID: pgstore.NewProfileID(), Name: name}); err != nil {
			t.Fatalf("profile %s on the main account while switchable: %v", name, err)
		}
	}
	var sam string
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 AND name = 'Sam'`, main).Scan(&sam); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := pgstore.MoveProfileInTx(ctx, tx, main, login.ID, sam, false); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("moving a profile to the login while switchable: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the move while switchable: %v", err)
	}
	var movedOrg, defaultOrg string
	if err := pool.QueryRow(ctx, `SELECT p.organization_id::text, public.bloem_default_organization_id()::text
		FROM user_profiles p WHERE p.user_id = $1 AND p.id = $2`, login.ID, sam).Scan(&movedOrg, &defaultOrg); err != nil {
		t.Fatalf("the moved profile: %v", err)
	}
	if movedOrg != defaultOrg {
		t.Fatalf("the moved profile sits in organization %s, want the default %s", movedOrg, defaultOrg)
	}

	for _, enabled := range []bool{false, true} {
		if err := users.Update(ctx, main, models.UpdateUserInput{Enabled: &enabled}); err != nil {
			t.Fatalf("setting the main account enabled=%t while switchable: %v", enabled, err)
		}
		var loginEnabled bool
		if err := pool.QueryRow(ctx, `SELECT enabled FROM users WHERE id = $1`, login.ID).Scan(&loginEnabled); err != nil {
			t.Fatal(err)
		}
		if loginEnabled != enabled {
			t.Fatalf("login enabled=%t after the main account was set enabled=%t; want it to follow", loginEnabled, enabled)
		}
	}
}
