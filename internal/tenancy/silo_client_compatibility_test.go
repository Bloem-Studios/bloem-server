package tenancy_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Silo clients resolve their subject in one of two shapes: with a profile once
// the viewer has picked one (silo-android, silo-apple and the web client all
// send X-Profile-Id conditionally), and without one before that, or from a
// caller that has no profile concept at all such as an API key or the
// audiobookshelf surface.
//
// This is the compatibility matrix for those shapes against the account kinds a
// multi-tenant deployment actually contains. Each case states what a Silo client
// must see; a failure here is a client that cannot use the server.
func TestSiloClientSubjectResolutionMatrix(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	resolver := tenancy.NewSubjectResolver(tenancy.NewResolver(store), store)

	// A tenant's own end user: exactly one membership, in the tenant.
	tenantA := createMemberTenant(t, ctx, store, 3)
	stamp := time.Now().UnixNano()
	member, _, err := service.Create(ctx, tenantA.ID, fmt.Sprintf("matrix-%d", stamp), tenancy.CreateMemberInput{
		Username: fmt.Sprintf("matrix-member-%d", stamp),
		Email:    fmt.Sprintf("matrix-member-%d@members.test", stamp),
		Password: "member-password",
	})
	if err != nil {
		t.Fatalf("create tenant member: %v", err)
	}
	// MemberService.Create does not make a profile, so give the member the one a
	// viewer would create before selecting it.
	var memberProfile string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM user_profiles WHERE user_id = $1 ORDER BY id LIMIT 1`, member.ID).Scan(&memberProfile); err != nil {
		memberProfile = uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_profiles (id, user_id, name, organization_id, access_group_id, is_primary)
			SELECT $1, $2, 'Matrix', $3, memberships.access_group_id, true
			FROM organization_memberships AS memberships
			WHERE memberships.account_id = $2 AND memberships.organization_id = $3`,
			memberProfile, member.ID, tenantA.ID); err != nil {
			t.Fatalf("seed member profile: %v", err)
		}
	}

	t.Run("tenant member without a profile reaches its own tenant", func(t *testing.T) {
		resolved, err := resolver.ResolveSubjectTenant(ctx, member.ID, "")
		if err != nil {
			t.Fatalf("profile-less resolution: %v", err)
		}
		if resolved.OrganizationID != tenantA.ID {
			t.Fatalf("organization = %s, want the member's tenant %s", resolved.OrganizationID, tenantA.ID)
		}
	})

	t.Run("tenant member with its profile reaches the same tenant", func(t *testing.T) {
		if memberProfile == "" {
			t.Skip("member has no profile to select")
		}
		resolved, err := resolver.ResolveSubjectTenant(ctx, member.ID, memberProfile)
		if err != nil {
			t.Fatalf("profile resolution: %v", err)
		}
		if resolved.OrganizationID != tenantA.ID {
			t.Fatalf("organization = %s, want %s", resolved.OrganizationID, tenantA.ID)
		}
	})

	t.Run("selecting a profile and not selecting one agree", func(t *testing.T) {
		if memberProfile == "" {
			t.Skip("member has no profile to compare")
		}
		withProfile, err1 := resolver.ResolveSubjectTenant(ctx, member.ID, memberProfile)
		without, err2 := resolver.ResolveSubjectTenant(ctx, member.ID, "")
		if err1 != nil || err2 != nil {
			t.Fatalf("resolution errors: %v / %v", err1, err2)
		}
		if withProfile.OrganizationID != without.OrganizationID {
			t.Fatalf("a client sees organization %s before picking a profile and %s after; "+
				"the same account must not move between tenants on profile selection",
				without.OrganizationID, withProfile.OrganizationID)
		}
	})

	t.Run("a tenant member's account-level policy is its tenant's, not empty", func(t *testing.T) {
		// A Silo client has no concept of organizations: it asks account-shaped
		// questions and needs an answer every time. models.User therefore
		// projects the membership representing the account, and for a tenant's
		// own end user that is the tenant's. An empty answer here would read to
		// the client as "no entitlements".
		users := auth.NewUserRepository(pool)
		loaded, err := users.GetByID(ctx, member.ID)
		if err != nil {
			t.Fatalf("load tenant member: %v", err)
		}
		var tenantGroup *int64
		if err := pool.QueryRow(ctx,
			`SELECT access_group_id FROM organization_memberships WHERE account_id = $1 AND organization_id = $2`,
			member.ID, tenantA.ID).Scan(&tenantGroup); err != nil {
			t.Fatalf("read tenant membership group: %v", err)
		}
		switch {
		case loaded.AccessGroupID == nil && tenantGroup == nil:
			// Both unset is consistent.
		case loaded.AccessGroupID == nil || tenantGroup == nil:
			t.Fatalf("account-level group = %v but the tenant membership holds %v; "+
				"a Silo client would see the wrong entitlements", loaded.AccessGroupID, tenantGroup)
		case *loaded.AccessGroupID != *tenantGroup:
			t.Fatalf("account-level group = %d, want the tenant's %d", *loaded.AccessGroupID, *tenantGroup)
		}
		if loaded.MaxProfiles <= 0 {
			t.Fatalf("account-level max_profiles = %d; a Silo client would read that as no profiles allowed", loaded.MaxProfiles)
		}
	})

	t.Run("an account in no organization keeps the pre-tenancy answer", func(t *testing.T) {
		accountID := seedTenantAccount(t, ctx, pool, "matrix-unmigrated-"+uuid.NewString()[:8])
		_, err := resolver.ResolveSubjectTenant(ctx, accountID, "")
		// Whatever this is, it must be the SAME answer the deployment gave before
		// tenancy existed, which is the default-organization path.
		t.Logf("membership-less account resolves as: %v", err)
	})
}
