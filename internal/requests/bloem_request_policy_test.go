package requests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/metadata/tmdb"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type requestPolicyFixture struct {
	pool                  *pgxpool.Pool
	account, otherAccount int
	org, otherOrg         uuid.UUID
	profile, blocked      string
	foreignOrg, foreign   string
	group, blockedGroup   int64
	accountGroup          int64
	foreignGroup          int64
}

// Rows are unique and cleanup is scoped; these tests share the migrated database
// with the integration gate and must never recreate or truncate it.
func newRequestPolicyFixture(t *testing.T) requestPolicyFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := requestPolicyFixture{pool: pool}
	suffix := uuid.NewString()
	for i, dest := range []*int{&f.account, &f.otherAccount} {
		if err := pool.QueryRow(t.Context(), `INSERT INTO users (username,email,password_hash,role,enabled)
			VALUES ($1,$2,'x','user',true) RETURNING id`, fmt.Sprintf("reqpolicy-%s-%d", suffix, i), fmt.Sprintf("reqpolicy-%s-%d@example.test", suffix, i)).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, command := range []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM media_requests WHERE requested_by_user_id=ANY($1)`, []any{[]int{f.account, f.otherAccount}}},
			{`DELETE FROM user_profiles WHERE user_id=ANY($1)`, []any{[]int{f.account, f.otherAccount}}},
			{`DELETE FROM organization_memberships WHERE account_id=ANY($1)`, []any{[]int{f.account, f.otherAccount}}},
			{`DELETE FROM resource_owners WHERE organization_id=ANY($1)`, []any{[]uuid.UUID{f.org, f.otherOrg}}},
			{`DELETE FROM organizations WHERE id=ANY($1)`, []any{[]uuid.UUID{f.org, f.otherOrg}}},
			{`DELETE FROM users WHERE id=ANY($1)`, []any{[]int{f.account, f.otherAccount}}},
			{`DELETE FROM access_groups WHERE id=$1`, []any{f.accountGroup}},
		} {
			if _, err := pool.Exec(ctx, command.sql, command.args...); err != nil {
				t.Errorf("request policy cleanup: %v", err)
			}
		}
	})
	for i, dest := range []*uuid.UUID{&f.org, &f.otherOrg} {
		if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (slug,name,status,owner_account_id)
			VALUES ($1,'Request policy test','active',$2) RETURNING id`, fmt.Sprintf("reqpolicy-%s-%d", suffix, i), f.account).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	var defaultOrg uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM organizations WHERE is_default`).Scan(&defaultOrg); err != nil {
		t.Fatal(err)
	}
	for i, row := range []struct {
		org     uuid.UUID
		allowed bool
		dest    *int64
	}{
		{f.org, true, &f.group}, {f.org, false, &f.blockedGroup}, {defaultOrg, true, &f.accountGroup}, {f.otherOrg, true, &f.foreignGroup},
	} {
		if err := pool.QueryRow(t.Context(), `INSERT INTO access_groups (organization_id,name,requests_allowed)
			VALUES ($1,$2,$3) RETURNING id`, row.org, fmt.Sprintf("reqpolicy-%s-%d", suffix, i), row.allowed).Scan(row.dest); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		org     uuid.UUID
		account int
		group   int64
	}{
		{f.org, f.account, f.blockedGroup}, {f.otherOrg, f.account, f.foreignGroup},
		{f.org, f.otherAccount, f.group}, {defaultOrg, f.account, f.accountGroup},
	} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role,access_group_id)
			SELECT $1,$2,'active','user',$3 WHERE set_config('bloem.membership_policy_writer',public.bloem_membership_policy_writer_marker(),true) IS NOT NULL
			ON CONFLICT (organization_id,account_id) DO UPDATE SET access_group_id=EXCLUDED.access_group_id`, row.org, row.account, row.group); err != nil {
			t.Fatal(err)
		}
	}
	for i, row := range []struct {
		org     uuid.UUID
		account int
		group   int64
		dest    *string
	}{
		{f.org, f.account, f.group, &f.profile}, {f.org, f.account, f.blockedGroup, &f.blocked},
		{f.otherOrg, f.account, f.foreignGroup, &f.foreignOrg}, {f.org, f.otherAccount, f.group, &f.foreign},
	} {
		*row.dest = fmt.Sprintf("reqpolicy-%s-%d", suffix, i)
		if _, err := pool.Exec(t.Context(), `INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id)
			VALUES ($1,$2,'Request policy profile',$3,$4)`, *row.dest, row.account, row.org, row.group); err != nil {
			t.Fatal(err)
		}
	}
	base := NewRepository(pool, nil)
	for _, row := range []struct{ group, max, window int64 }{{f.group, 3, 14}, {f.blockedGroup, 1, 21}, {f.accountGroup, 90, 30}} {
		if _, err := base.UpsertGroupLimitConditional(t.Context(), GroupLimit{GroupID: row.group, LimitMode: LimitModeCustom, MaxRequests: intPtr(int(row.max)), WindowDays: intPtr(int(row.window)), ApprovalMode: ApprovalModeManual}, 0); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f requestPolicyFixture) context(t *testing.T, org uuid.UUID, profile string) context.Context {
	t.Helper()
	var tenant tenancy.Context
	if err := f.pool.QueryRow(t.Context(), `SELECT o.id,o.status,o.is_default,o.policy_revision,m.id,m.status,m.security_revision
		FROM organizations o JOIN organization_memberships m ON m.organization_id=o.id WHERE o.id=$1 AND m.account_id=$2`, org, f.account).Scan(
		&tenant.OrganizationID, &tenant.OrganizationStatus, &tenant.OrganizationDefault, &tenant.PolicyRevision,
		&tenant.MembershipID, &tenant.MembershipStatus, &tenant.SecurityRevision); err != nil {
		t.Fatal(err)
	}
	tenant.AccountID = f.account
	tenant.Legacy = tenant.OrganizationDefault
	ctx := tenancy.WithContext(t.Context(), tenant)
	return access.SetScope(ctx, access.Scope{UserID: f.account, ProfileID: profile, ProfileVerified: true})
}

func (f requestPolicyFixture) service(t *testing.T) *Service {
	t.Helper()
	service := NewService(NewRepository(f.pool, nil), &fakeTMDBClient{}, &fakePresence{})
	service.SetUserRepository(auth.NewUserRepository(f.pool))
	service.SetGroupPolicyProvider(access.NewTenantGroupStore(f.pool))
	if _, err := NewTenantGroupLimitService(service); err != nil {
		t.Fatal(err)
	}
	if err := service.EnableTenantRequestPolicy(); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestBloemRequestPolicyUsesVerifiedProfileGroupDatabase(t *testing.T) {
	f := newRequestPolicyFixture(t)
	service := f.service(t)
	for _, tc := range []struct {
		name, profile string
		blocked       bool
		max, window   int
	}{
		{"allowed profile", f.profile, false, 3, 14}, {"blocked sibling", f.blocked, true, 1, 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := f.context(t, f.org, tc.profile)
			policy, err := service.EffectivePolicy(ctx, f.account)
			if err != nil {
				t.Fatalf("verified profile policy: %v", err)
			}
			if policy.Blocked != tc.blocked || policy.MaxRequests != tc.max || policy.WindowDays != tc.window || policy.AutoApprove {
				t.Fatalf("profile permission/quota = %+v; want blocked=%v max=%d window=%d manual", policy, tc.blocked, tc.max, tc.window)
			}
			allowed, err := service.RequestCapabilityAllowed(ctx, Viewer{UserID: f.account, ProfileID: tc.profile})
			if err != nil || allowed == tc.blocked {
				t.Fatalf("profile request capability = %v, %v", allowed, err)
			}
		})
	}
}

func TestBloemRequestPolicyRejectsInvalidProfileAuthorityDatabase(t *testing.T) {
	f := newRequestPolicyFixture(t)
	service := f.service(t)
	valid := f.context(t, f.org, f.profile)
	tenant, _ := tenancy.FromContext(valid)
	falseDefault := tenant
	falseDefault.Legacy, falseDefault.OrganizationDefault = true, true
	suspendedContext := tenant
	suspendedContext.MembershipStatus = tenancy.MembershipSuspended
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"no tenant", access.SetScope(t.Context(), access.Scope{UserID: f.account, ProfileID: f.profile, ProfileVerified: true})},
		{"claims alone", auth.WithClaims(tenancy.WithContext(t.Context(), tenant), &auth.Claims{UserID: f.account, ProfileID: f.profile})},
		{"unverified profile", access.SetScope(valid, access.Scope{UserID: f.account, ProfileID: f.profile})},
		{"another account scope", access.SetScope(valid, access.Scope{UserID: f.otherAccount, ProfileID: f.profile, ProfileVerified: true})},
		{"profile in another organization", f.context(t, f.org, f.foreignOrg)},
		{"another account profile", f.context(t, f.org, f.foreign)},
		{"missing profile", f.context(t, f.org, "missing-"+uuid.NewString())},
		{"profile-less native request", f.context(t, f.org, "")},
		{"hosted organization marked default", tenancy.WithContext(t.Context(), falseDefault)},
		{"inactive resolved membership", access.SetScope(tenancy.WithContext(t.Context(), suspendedContext), access.Scope{UserID: f.account, ProfileID: f.profile, ProfileVerified: true})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.EffectivePolicy(tc.ctx, f.account); !errors.Is(err, ErrForbidden) {
				t.Fatalf("invalid profile authority = %v, want ErrForbidden", err)
			}
		})
	}
	if err := service.ensureViewerRequestsAllowed(valid, Viewer{UserID: f.account, ProfileID: f.blocked}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer profile mismatch accepted: %v", err)
	}
	if _, err := service.EffectivePolicy(valid, f.otherAccount); !errors.Is(err, ErrForbidden) {
		t.Fatalf("account mismatch accepted: %v", err)
	}
	for _, tc := range []struct {
		name, sql string
	}{
		{"inactive membership", `UPDATE organization_memberships SET status='suspended' WHERE organization_id=$1 AND account_id=$2`},
		{"suspended organization", `UPDATE organizations SET status='suspended' WHERE id=$1`},
		{"stale organization revision", `UPDATE organizations SET policy_revision=policy_revision+1 WHERE id=$1`},
		{"stale membership revision", `UPDATE organization_memberships SET security_revision=security_revision+1 WHERE organization_id=$1 AND account_id=$2`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []any{f.org}
			if tc.name == "inactive membership" || tc.name == "stale membership revision" {
				args = append(args, f.account)
			}
			// A committed targeted mutation exercises stale validated context.
			if _, err := f.pool.Exec(t.Context(), tc.sql, args...); err != nil {
				t.Fatal(err)
			}
			if _, err := service.EffectivePolicy(valid, f.account); !errors.Is(err, ErrForbidden) {
				t.Fatalf("stale/inactive authority = %v, want ErrForbidden", err)
			}
			if _, err := f.pool.Exec(t.Context(), `UPDATE organizations SET status=$2,policy_revision=$3 WHERE id=$1`, f.org, tenant.OrganizationStatus, tenant.PolicyRevision); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(t.Context(), `UPDATE organization_memberships SET status=$3,security_revision=$4 WHERE organization_id=$1 AND account_id=$2`, f.org, f.account, tenant.MembershipStatus, tenant.SecurityRevision); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE users SET enabled=false WHERE id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EffectivePolicy(valid, f.account); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled account authority = %v, want ErrForbidden", err)
	}
}

func TestBloemRequestPolicyPreservesLegacyAndOrdinarySiloDatabase(t *testing.T) {
	f := newRequestPolicyFixture(t)
	service := f.service(t)
	var defaultOrg uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM organizations WHERE is_default`).Scan(&defaultOrg); err != nil {
		t.Fatal(err)
	}
	ctx := f.context(t, defaultOrg, "")
	policy, err := service.EffectivePolicy(ctx, f.account)
	if err != nil || policy.Blocked || policy.MaxRequests != 90 || policy.WindowDays != 30 {
		t.Fatalf("profile-less legacy policy = %+v, %v", policy, err)
	}
	legacy, _ := tenancy.FromContext(ctx)
	policy, err = service.EffectivePolicy(tenancy.WithContext(t.Context(), legacy), f.account)
	if err != nil || policy.Blocked || policy.MaxRequests != 90 || policy.WindowDays != 30 {
		t.Fatalf("legacy account policy without viewer scope = %+v, %v", policy, err)
	}
	ordinary := NewService(NewRepository(f.pool, nil), nil, nil)
	ordinary.SetUserRepository(auth.NewUserRepository(f.pool))
	ordinary.SetGroupPolicyProvider(requestGroupProvider{group: &access.GroupPolicy{RequestsAllowed: true}})
	policy, err = ordinary.EffectivePolicy(t.Context(), f.account)
	if err != nil || policy.Blocked || policy.MaxRequests != 90 || policy.WindowDays != 30 {
		t.Fatalf("ordinary Silo account policy = %+v, %v", policy, err)
	}
	user, err := auth.NewUserRepository(f.pool).GetByID(t.Context(), f.account)
	if err != nil || user.Role != models.RoleUser || user.AccessGroupID == nil || *user.AccessGroupID != f.accountGroup {
		t.Fatalf("stored account projection was changed: %+v, %v", user, err)
	}
}

func TestBloemRequestPolicyMemberOverrideAndUserLimitPrecedenceDatabase(t *testing.T) {
	f := newRequestPolicyFixture(t)
	service := f.service(t)
	ctx := f.context(t, f.org, f.profile)
	// A different membership must not supply this tenant's account override.
	if _, err := f.pool.Exec(t.Context(), `UPDATE organization_memberships SET requests_allowed=false
		WHERE account_id=$1 AND organization_id<>$2
		AND set_config('bloem.membership_policy_writer',public.bloem_membership_policy_writer_marker(),true) IS NOT NULL`, f.account, f.org); err != nil {
		t.Fatal(err)
	}
	policy, err := service.EffectivePolicy(ctx, f.account)
	if err != nil || policy.Blocked {
		t.Fatalf("another membership's deny leaked: %+v, %v", policy, err)
	}
	for _, allowed := range []bool{false, true} {
		if _, err := f.pool.Exec(t.Context(), `UPDATE organization_memberships SET requests_allowed=$3
			WHERE organization_id=$1 AND account_id=$2
			AND set_config('bloem.membership_policy_writer',public.bloem_membership_policy_writer_marker(),true) IS NOT NULL`, f.org, f.account, allowed); err != nil {
			t.Fatal(err)
		}
		ctx = f.context(t, f.org, f.blocked)
		policy, err = service.EffectivePolicy(ctx, f.account)
		if err != nil || policy.Blocked == allowed || policy.MaxRequests != 1 || policy.WindowDays != 21 {
			t.Fatalf("membership override=%v policy=%+v error=%v", allowed, policy, err)
		}
		capability, err := service.RequestCapabilityAllowed(ctx, Viewer{UserID: f.account, ProfileID: f.blocked})
		if err != nil || capability != allowed {
			t.Fatalf("membership override capability=%v error=%v", capability, err)
		}
	}
	if _, err := NewRepository(f.pool, nil).UpsertUserLimit(t.Context(), UserLimit{UserID: f.account, LimitMode: LimitModeCustom, MaxRequests: intPtr(8), WindowDays: intPtr(5), ApprovalMode: ApprovalModeAuto}); err != nil {
		t.Fatal(err)
	}
	policy, err = service.EffectivePolicy(ctx, f.account)
	if err != nil || policy.Blocked || policy.MaxRequests != 8 || policy.WindowDays != 5 || !policy.AutoApprove {
		t.Fatalf("account limit precedence=%+v error=%v", policy, err)
	}
}

func TestBloemRequestPolicyQuotaCountsAndInternalEnrichmentDatabase(t *testing.T) {
	f := newRequestPolicyFixture(t)
	service := f.service(t)
	ctx := f.context(t, f.org, f.profile)
	tmdbID := int(uuid.New().ID() & 0x7fffffff) // TMDB IDs are stored in PostgreSQL int4.
	if tmdbID == 0 {
		tmdbID = 1
	}
	requestID := "reqpolicy-" + uuid.NewString()
	if _, err := NewRepository(f.pool, nil).CreateRequest(ctx, CreateRequestRecord{
		ID: requestID, Input: CreateRequestInput{MediaType: MediaTypeMovie, TMDBID: tmdbID, Title: "Request quota test"},
		Requester: Viewer{UserID: f.account, ProfileID: f.profile},
	}); err != nil {
		t.Fatal(err)
	}
	policy, err := service.EffectivePolicy(ctx, f.account)
	if err != nil || policy.MaxRequests != 3 || policy.Used != 1 || policy.Remaining != 2 {
		t.Fatalf("active-profile quota usage=%+v error=%v", policy, err)
	}
	page, err := service.enrichPage(ctx, Viewer{UserID: f.account, ProfileID: f.profile}, &tmdb.MediaPage{
		Page: 1, TotalPages: 1, TotalResults: 1,
		Results: []tmdb.MediaResult{{ID: tmdbID, MediaType: "movie", Title: "Request quota test"}},
	})
	if err != nil || page == nil || len(page.Results) != 1 || page.Results[0].Request.RequestID != requestID {
		t.Fatalf("internal enrichment lost profile policy: page=%+v error=%v", page, err)
	}
}
