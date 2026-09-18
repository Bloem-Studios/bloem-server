package invitations

import (
	"context"
	"errors"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestOrganizationInvitationLifecycleFencesTenantRoleAndOldLinks(t *testing.T) {
	ctx := context.Background()
	pool := newInvitationOrganizationDatabase(t, ctx)
	repo := NewRepository(pool)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var org uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT public.bloem_default_organization_id()`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	var actor int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role) VALUES('lifecycle','lifecycle@example.test','hash','admin') RETURNING id`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	makeInvite := func(email, role string) *models.Invitation {
		t.Helper()
		v, err := repo.CreateForOrganization(ctx, org, models.CreateInvitationInput{Email: email, Role: role, InvitedBy: actor, ExpiresAt: time.Now().Add(time.Hour), CreateProfile: true}, email+"-hash")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	initial := makeInvite("reader@example.test", "user")
	foreign := uuid.New()
	if _, err := repo.ResendForOrganization(ctx, foreign, initial.ID, actor, "foreign", time.Now().Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign rotation: %v", err)
	}
	if err := repo.RevokeForOrganization(ctx, foreign, initial.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign revoke: %v", err)
	}
	rotated, err := repo.ResendForOrganization(ctx, org, initial.ID, actor, "replacement-hash", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ID == initial.ID || rotated.Email != initial.Email || !rotated.CreateProfile {
		t.Fatalf("replacement = %+v", rotated)
	}
	old, err := repo.GetByTokenHash(ctx, initial.Email+"-hash")
	if err != nil || old.RevokedAt == nil {
		t.Fatalf("old link not revoked: %+v %v", old, err)
	}
	if _, err := repo.ResendForOrganization(ctx, org, initial.ID, actor, "stale-hash", time.Now().Add(time.Hour)); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("stale resend: %v", err)
	}
	results := make(chan error, 2)
	for _, hash := range []string{"concurrent-a", "concurrent-b"} {
		go func(hash string) {
			_, err := repo.ResendForOrganization(ctx, org, rotated.ID, actor, hash, time.Now().Add(time.Hour))
			results <- err
		}(hash)
	}
	successes := 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrNotClaimable) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent rotations succeeded %d times", successes)
	}
	pending := makeInvite("revoke@example.test", "user")
	if err := repo.RevokeForOrganization(ctx, org, pending.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResendForOrganization(ctx, org, pending.ID, actor, "revived", time.Now().Add(time.Hour)); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("revived revoked invitation: %v", err)
	}
	platform := makeInvite("operator@example.test", "admin")
	if _, err := repo.ResendForOrganization(ctx, org, platform.ID, actor, "operator-rotation", time.Now().Add(time.Hour)); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("rotated platform invitation: %v", err)
	}
	if err := repo.RevokeForOrganization(ctx, org, platform.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked platform invitation: %v", err)
	}
}
