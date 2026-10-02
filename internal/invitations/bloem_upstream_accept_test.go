package invitations

import (
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
)

func TestBloemLinkInvitationRetainsOrganizationAndScopedSupersession(t *testing.T) {
	f := atomicInvitationDB(t)
	ctx := t.Context()
	organizationID := uuid.New()
	if _, err := f.pool.Exec(ctx, `INSERT INTO organizations(id,slug,name,status,owner_account_id) VALUES($1,$2,'Inviting organization','active',1)`, organizationID, organizationID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO access_groups(organization_id,name,is_default) VALUES($1,'Default',true)`, organizationID); err != nil {
		t.Fatal(err)
	}
	input := models.CreateInvitationInput{Email: "scoped@example.invalid", Role: models.RoleUser, CreateProfile: true, InvitedBy: 1, ExpiresAt: time.Now().Add(time.Hour)}
	outside, err := f.repo.Create(ctx, input, HashToken("outside"))
	if err != nil {
		t.Fatal(err)
	}
	inside, err := f.repo.CreateForOrganization(ctx, organizationID, input, HashToken("inside"))
	if err != nil {
		t.Fatal(err)
	}
	input.Email = ""
	input.Delivery = models.InvitationDeliveryLink
	link, err := f.repo.CreateForOrganization(ctx, organizationID, input, HashToken("link"))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(f.repo, auth.NewUserRepository(f.pool), f.accounts(pgstore.NewPostgresProvider(f.pool)), &fakeSessions{err: errors.New("login deliberately unavailable")}, nil, nil, nil, "")
	_, user, err := svc.Accept(ctx, "link", "scoped@example.invalid", "fixture-password", "test", "")
	if !errors.Is(err, ErrSessionStart) || user == nil {
		t.Fatalf("accept user=%v error=%v", user, err)
	}
	var memberships, profiles int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM organization_memberships WHERE account_id=$1 AND organization_id=$2),(SELECT count(*) FROM user_profiles WHERE user_id=$1 AND organization_id=$2)`, user.ID, organizationID).Scan(&memberships, &profiles); err != nil {
		t.Fatal(err)
	}
	if memberships != 1 || profiles != 1 {
		t.Fatalf("inviting organization memberships=%d profiles=%d", memberships, profiles)
	}
	var foreignMemberships int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE account_id=$1 AND organization_id<>$2`, user.ID, organizationID).Scan(&foreignMemberships); err != nil || foreignMemberships != 0 {
		t.Fatalf("foreign memberships=%d error=%v", foreignMemberships, err)
	}
	for _, check := range []struct {
		id      int64
		revoked bool
	}{{outside.ID, false}, {inside.ID, true}} {
		var revoked bool
		if err := f.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM invitations WHERE id=$1`, check.id).Scan(&revoked); err != nil || revoked != check.revoked {
			t.Fatalf("invitation %d revoked=%t want=%t error=%v", check.id, revoked, check.revoked, err)
		}
	}
	var acceptedEmail string
	if err := f.pool.QueryRow(ctx, `SELECT email FROM invitations WHERE id=$1 AND accepted_user_id=$2`, link.ID, user.ID).Scan(&acceptedEmail); err != nil || acceptedEmail != user.Email {
		t.Fatalf("accepted email=%q error=%v", acceptedEmail, err)
	}
}
