package organizations_test

import (
	"errors"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5"
	"testing"
	"time"
)

func TestInviteCodeBindsAccountOrganization(t *testing.T) {
	pool := ownershipDatabase(t)
	ctx := t.Context()
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var a, b int64
	for slug, id := range map[string]*int64{"a": &a, "b": &b} {
		if err := pool.QueryRow(ctx, `INSERT INTO organizations(slug,name) VALUES ($1,$1) RETURNING id`, slug).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	users := auth.NewUserRepository(pool)
	creator, err := users.Create(ctx, models.CreateUserInput{Username: "creator", Email: "creator@example.test", Password: "fixture-password", Role: models.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO invite_codes(code,max_uses,created_by,organization_id) VALUES ('tenant-invite',3,$1,$2)`, creator.ID, a); err != nil {
		t.Fatal(err)
	}
	codes, err := auth.NewInviteCodeRepository(pool).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 1 || codes[0].OrganizationID != a {
		t.Fatal("invite listing lost organization ownership")
	}
	named := models.CreateInviteCodeInput{Code: "named", Label: "Named", MaxUses: 1, CreatedBy: creator.ID, OrganizationID: a}
	if _, err := auth.NewInviteCodeRepository(pool).CreateNamed(ctx, named); err != nil {
		t.Fatal(err)
	}
	named.OrganizationID = b
	if _, err := auth.NewInviteCodeRepository(pool).CreateNamed(ctx, named); !errors.Is(err, auth.ErrInviteCodeConflict) {
		t.Fatalf("named code changed organization: %v", err)
	}
	in := models.CreateUserInput{OrganizationID: b, Username: "member", Email: "member@example.test", Password: "fixture-password", Role: models.RoleUser}
	user, err := users.CreateInvited(ctx, in, "tenant-invite", func(*models.User, pgx.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if user.OrganizationID != a {
		t.Fatalf("invite organization overridden: %d", user.OrganizationID)
	}
	in.Username = "escalated"
	in.Email = "escalated@example.test"
	in.Role = models.RoleAdmin
	if _, err := users.CreateInvited(ctx, in, "tenant-invite", func(*models.User, pgx.Tx) error { return nil }); err == nil {
		t.Fatal("invite code created a platform administrator")
	}
	var uses int
	if err := pool.QueryRow(ctx, `SELECT use_count FROM invite_codes WHERE code='tenant-invite'`).Scan(&uses); err != nil {
		t.Fatal(err)
	}
	if uses != 1 {
		t.Fatalf("failed redemption consumed a use: %d", uses)
	}
}

func TestEmailInvitationsStayInOrganization(t *testing.T) {
	pool := ownershipDatabase(t)
	ctx := t.Context()
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var a, b int64
	for slug, id := range map[string]*int64{"a": &a, "b": &b} {
		if err := pool.QueryRow(ctx, `INSERT INTO organizations(slug,name) VALUES ($1,$1) RETURNING id`, slug).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	creator, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{Username: "creator", Email: "creator@example.test", Password: "fixture-password", Role: models.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	repo := invitations.NewRepository(pool)
	input := models.CreateInvitationInput{OrganizationID: a, Email: "invitee@example.test", Role: models.RoleUser, InvitedBy: int64(creator.ID), ExpiresAt: time.Now().Add(time.Hour)}
	first, err := repo.Create(ctx, input, "hash-a")
	if err != nil {
		t.Fatal(err)
	}
	input.OrganizationID = b
	if _, err := repo.Create(ctx, input, "hash-b"); err != nil {
		t.Fatal(err)
	}
	still, err := repo.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.RevokedAt != nil {
		t.Fatal("another organization superseded the invitation")
	}
	resent, err := repo.Resend(ctx, first.ID, input, "hash-a-new")
	if err != nil {
		t.Fatal(err)
	}
	if resent.OrganizationID != a {
		t.Fatal("resend changed organization")
	}
	in := models.CreateUserInput{OrganizationID: b, Username: "wrong-org", Email: "wrong@example.test", Password: "fixture-password", Role: models.RoleUser}
	_, err = repo.Accept(ctx, "hash-a-new", func(_ *models.Invitation, tx pgx.Tx) (*models.User, error) {
		return auth.NewAccountProvisioner(auth.NewUserRepository(pool), nil).CreateInitialAccountInTransaction(ctx, tx, auth.CreateAccountInput{User: in})
	})
	if err == nil {
		t.Fatal("invitation accepted by a different organization's account")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE username='wrong-org'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed acceptance left an account behind")
	}
}
