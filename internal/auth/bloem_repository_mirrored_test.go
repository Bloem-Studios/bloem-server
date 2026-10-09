package auth_test

// Bloem's own account writes on a database mirrored for Silo switching: they
// must succeed (compatibility froze them) and reach the users policy columns
// that upstream Silo reads.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// bloemMirroredDatabase holds one account Silo created, then enables switching.
func bloemMirroredDatabase(t *testing.T) (context.Context, *pgxpool.Pool, int) {
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

func TestBloemAccountPolicyUpdateMirrorsToUsersWhenMirrored(t *testing.T) {
	ctx, pool, account := bloemMirroredDatabase(t)
	users := auth.NewUserRepository(pool)
	if err := users.Update(ctx, account, models.UpdateUserInput{
		MaxStreams:       models.SetValue(6),
		TranscodeAllowed: models.SetValue(false),
	}); err != nil {
		t.Fatalf("bloem policy update while mirrored: %v", err)
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

func TestBloemAccountCreateWhenMirrored(t *testing.T) {
	ctx, pool, _ := bloemMirroredDatabase(t)
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
		t.Fatalf("bloem create account while mirrored: %v", err)
	}
	var memberships, userStreams, membershipStreams int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM organization_memberships WHERE account_id = $1),
		       (SELECT max_streams FROM users WHERE id = $1),
		       (SELECT max_streams FROM organization_memberships WHERE account_id = $1)`, created.ID,
	).Scan(&memberships, &userStreams, &membershipStreams); err != nil {
		t.Fatal(err)
	}
	if memberships != 1 {
		t.Fatalf("memberships = %d, want 1", memberships)
	}
	if userStreams != 4 || membershipStreams != 4 {
		t.Fatalf("max_streams users=%d membership=%d, want 4 and 4", userStreams, membershipStreams)
	}
}
