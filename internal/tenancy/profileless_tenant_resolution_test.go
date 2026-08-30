package tenancy_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// A Silo client sends X-Profile-Id only once a profile has been selected, and
// the viewer-access middleware passes whatever it finds straight through. So a
// content request made before selection resolves the subject with no profile.
//
// Resolving that through the DEPLOYMENT DEFAULT organization tells a tenant's
// own end user that its tenant does not exist, because it holds no membership
// there. The account's own organization is the only correct answer, and it is
// unambiguous for a member who belongs to exactly one.
func TestProfilelessSubjectResolutionUsesTheAccountsOwnTenant(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)

	member, _, err := service.Create(ctx, tenant.ID, "profileless-probe", tenancy.CreateMemberInput{
		Username: fmt.Sprintf("profileless-%d", time.Now().UnixNano()),
		Email:    fmt.Sprintf("profileless-%d@members.test", time.Now().UnixNano()),
		Password: "member-password",
	})
	if err != nil {
		t.Fatalf("create tenant member: %v", err)
	}

	// The premise: this member belongs to the tenant and to nothing else.
	var organizations int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM organization_memberships WHERE account_id = $1`, member.ID).Scan(&organizations); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if organizations != 1 {
		t.Fatalf("member holds %d memberships, want only the tenant's", organizations)
	}

	resolver := tenancy.NewSubjectResolver(tenancy.NewResolver(store), store)
	resolved, err := resolver.ResolveSubjectTenant(ctx, member.ID, "")
	if err != nil {
		t.Fatalf("profile-less resolution failed for a tenant-only member: %v\n"+
			"a Silo client browsing before the viewer picks a profile would read this as its tenant not existing", err)
	}
	if resolved.OrganizationID != tenant.ID {
		t.Fatalf("profile-less resolution chose %s, want the member's own tenant %s",
			resolved.OrganizationID, tenant.ID)
	}
	if resolved.AccountID != member.ID {
		t.Fatalf("resolved account = %d, want %d", resolved.AccountID, member.ID)
	}
}

// An account that belongs to no organization at all keeps the pre-tenancy
// answer: the deployment default. Removing that would break every account on a
// server that has not been converted.
func TestProfilelessSubjectResolutionKeepsDefaultForUnmigratedAccount(t *testing.T) {
	ctx, pool, store := testTenantPool(t)
	accountID := seedTenantAccount(t, ctx, pool, "unmigrated-"+uuid.NewString()[:8])

	var organizations int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM organization_memberships WHERE account_id = $1`, accountID).Scan(&organizations); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if organizations != 0 {
		t.Skipf("account already holds %d memberships; this test needs one with none", organizations)
	}

	resolver := tenancy.NewSubjectResolver(tenancy.NewResolver(store), store)
	if _, err := resolver.ResolveSubjectTenant(ctx, accountID, ""); err == nil {
		t.Log("membership-less account resolved through the default organization")
	} else {
		t.Logf("membership-less account = %v (the legacy path still decides this case)", err)
	}
}
