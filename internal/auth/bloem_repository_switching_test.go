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
	accounts.SetMembershipProvisioner(&bloemAccountMemberships{store: tenancy.NewStore(pool)})
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
