package requests

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func groupLimitTestTenant(accountID int, org uuid.UUID, isDefault bool) tenancy.Context {
	tenant := tenancy.Context{
		AccountID: accountID, OrganizationID: org, MembershipID: uuid.New(),
		OrganizationStatus: tenancy.OrganizationActive, MembershipStatus: tenancy.MembershipActive,
		PolicyRevision: 7, SecurityRevision: 11, OrganizationDefault: isDefault,
	}
	if isDefault {
		tenant.Legacy, tenant.OrganizationStatus = true, tenancy.OrganizationInitializing
	}
	return tenant
}

func TestBloemGroupLimitsOrganizationBoundaryDatabase(t *testing.T) {
	f := newRequestOrgFixture(t)
	base := NewRepository(f.pool, nil)
	service, err := NewTenantGroupLimitService(NewService(base, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{UserID: f.accountT, IsAdmin: true}
	var defaultOrg uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM organizations WHERE is_default`).Scan(&defaultOrg); err != nil {
		t.Fatal(err)
	}
	orgs := []uuid.UUID{f.orgT, f.orgE, defaultOrg}
	groups := make([]int64, len(orgs))
	for i, org := range orgs {
		if err := f.pool.QueryRow(t.Context(), `INSERT INTO access_groups (organization_id,name) VALUES ($1,$2) RETURNING id`, org, fmt.Sprintf("request-limit-%d", i)).Scan(&groups[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := base.UpsertGroupLimitConditional(t.Context(), GroupLimit{GroupID: groups[i], LimitMode: LimitModeCustom, MaxRequests: intPtr(5), WindowDays: intPtr(7), ApprovalMode: ApprovalModeManual}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for i, org := range orgs {
		ctx := tenancy.WithContext(t.Context(), groupLimitTestTenant(f.accountT, org, org == defaultOrg))
		for j, groupID := range groups {
			t.Run(fmt.Sprintf("organization-%d/group-%d", i, j), func(t *testing.T) {
				read, err := service.GetGroupLimit(ctx, viewer, groupID)
				if i == j {
					if err != nil || read == nil || read.GroupID != groupID || read.MaxRequests == nil || *read.MaxRequests != 5 {
						t.Fatalf("own group limit: %+v, %v", read, err)
					}
					return
				}
				if !errors.Is(err, ErrNotFound) {
					t.Errorf("foreign group read = %+v, %v; want ErrNotFound", read, err)
				}
				store := service.store.(GroupLimitStore)
				if exists, err := store.GroupExists(ctx, groupID); err != nil || exists {
					t.Errorf("foreign group existence = %v, %v; want false", exists, err)
				}
				if _, err := store.GetGroupLimit(ctx, groupID); !errors.Is(err, ErrNotFound) {
					t.Errorf("direct foreign group read = %v, want ErrNotFound", err)
				}
				if _, err := service.UpsertGroupLimitConditional(ctx, viewer, GroupLimit{GroupID: groupID, LimitMode: LimitModeUnlimited, ApprovalMode: ApprovalModeAuto}, -1); !errors.Is(err, ErrNotFound) {
					t.Errorf("foreign group overwrite = %v, want ErrNotFound", err)
				}
				unchanged, err := base.GetGroupLimit(t.Context(), groupID)
				if err != nil || unchanged == nil || unchanged.LimitMode != LimitModeCustom || unchanged.ApprovalMode != ApprovalModeManual {
					t.Fatalf("foreign group limit changed: %+v, %v", unchanged, err)
				}
				var unsavedID int64
				if err := f.pool.QueryRow(t.Context(), `INSERT INTO access_groups (organization_id,name) VALUES ($1,$2) RETURNING id`, orgs[j], fmt.Sprintf("unsaved-limit-%d-%d", i, j)).Scan(&unsavedID); err != nil {
					t.Fatal(err)
				}
				if _, err := service.UpsertGroupLimitConditional(ctx, viewer, GroupLimit{GroupID: unsavedID, LimitMode: LimitModeUnlimited, ApprovalMode: ApprovalModeAuto}, 0); !errors.Is(err, ErrNotFound) {
					t.Errorf("foreign first save = %v, want ErrNotFound", err)
				}
				if limit, err := base.GetGroupLimit(t.Context(), unsavedID); err != nil || limit != nil {
					t.Fatalf("foreign limit created: %+v, %v", limit, err)
				}
			})
		}
	}
}

func TestBloemGroupLimitScopeAndRevisionDatabase(t *testing.T) {
	f := newRequestOrgFixture(t)
	base := NewRepository(f.pool, nil)
	service, err := NewTenantGroupLimitService(NewService(base, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{UserID: f.accountT, IsAdmin: true}
	var groupID int64
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO access_groups (organization_id,name) VALUES ($1,'Unsaved request limit') RETURNING id`, f.orgT).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	in := GroupLimit{GroupID: groupID, LimitMode: LimitModeUnlimited, ApprovalMode: ApprovalModeAuto}
	for i, ctx := range []context.Context{t.Context(), tenancy.WithContext(t.Context(), tenancy.Context{AccountID: f.accountT}), tenancy.WithContext(t.Context(), tenancy.Context{OrganizationID: f.orgT})} {
		t.Run(fmt.Sprintf("missing-scope-%d", i), func(t *testing.T) {
			if _, err := service.GetGroupLimit(ctx, viewer, groupID); !errors.Is(err, ErrForbidden) {
				t.Errorf("unscoped read = %v, want ErrForbidden", err)
			}
			if _, err := service.UpsertGroupLimitConditional(ctx, viewer, in, -1); !errors.Is(err, ErrForbidden) {
				t.Errorf("unscoped write = %v, want ErrForbidden", err)
			}
			if _, err := service.store.(GroupLimitStore).GroupExists(ctx, groupID); !errors.Is(err, ErrForbidden) {
				t.Errorf("unscoped group existence = %v, want ErrForbidden", err)
			}
		})
	}
	valid := groupLimitTestTenant(f.accountT, f.orgT, false)
	for _, tc := range []struct {
		name   string
		mutate func(*tenancy.Context)
	}{
		{"missing membership", func(v *tenancy.Context) { v.MembershipID = uuid.Nil }},
		{"inactive membership", func(v *tenancy.Context) { v.MembershipStatus = tenancy.MembershipSuspended }},
		{"missing policy revision", func(v *tenancy.Context) { v.PolicyRevision = 0 }},
		{"missing security revision", func(v *tenancy.Context) { v.SecurityRevision = 0 }},
		{"suspended organization", func(v *tenancy.Context) { v.OrganizationStatus = tenancy.OrganizationSuspended }},
		{"initializing hosted organization", func(v *tenancy.Context) { v.OrganizationStatus, v.Legacy = tenancy.OrganizationInitializing, true }},
		{"native initializing default", func(v *tenancy.Context) {
			v.OrganizationStatus, v.OrganizationDefault = tenancy.OrganizationInitializing, true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant := valid
			tc.mutate(&tenant)
			ctx := tenancy.WithContext(t.Context(), tenant)
			if _, err := service.GetGroupLimit(ctx, viewer, groupID); !errors.Is(err, ErrForbidden) {
				t.Errorf("invalid tenant read = %v, want ErrForbidden", err)
			}
			if _, err := service.UpsertGroupLimitConditional(ctx, viewer, in, -1); !errors.Is(err, ErrForbidden) {
				t.Errorf("invalid tenant write = %v, want ErrForbidden", err)
			}
			if _, err := service.store.(GroupLimitStore).GroupExists(ctx, groupID); !errors.Is(err, ErrForbidden) {
				t.Errorf("invalid tenant existence = %v, want ErrForbidden", err)
			}
		})
	}
	ctx := tenancy.WithContext(t.Context(), valid)
	mismatched := Viewer{UserID: f.accountE, IsAdmin: true}
	if _, err := service.GetGroupLimit(ctx, mismatched, groupID); !errors.Is(err, ErrForbidden) {
		t.Errorf("another viewer's tenant read = %v, want ErrForbidden", err)
	}
	if _, err := service.UpsertGroupLimitConditional(ctx, mismatched, in, -1); !errors.Is(err, ErrForbidden) {
		t.Errorf("another viewer's tenant write = %v, want ErrForbidden", err)
	}
	if _, err := service.GetGroupLimit(ctx, Viewer{UserID: f.accountT}, groupID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin read = %v", err)
	}
	if _, err := service.UpsertGroupLimitConditional(ctx, Viewer{UserID: f.accountT}, in, -1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin write = %v", err)
	}
	fresh, err := service.GetGroupLimit(ctx, viewer, groupID)
	if err != nil || fresh == nil || fresh.Revision != 0 || fresh.LimitMode != LimitModeInherit || fresh.ApprovalMode != ApprovalModeInherit {
		t.Fatalf("unsaved group inheritance = %+v, %v", fresh, err)
	}
	saved, err := service.UpsertGroupLimitConditional(ctx, viewer, in, 0)
	if err != nil || saved == nil || saved.Revision <= 0 {
		t.Fatalf("first limit save = %+v, %v", saved, err)
	}
	if _, err := service.UpsertGroupLimitConditional(ctx, viewer, in, 0); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale first save = %v, want ErrStaleRevision", err)
	}
	in.LimitMode, in.ApprovalMode = LimitModeInherit, ApprovalModeManual
	updated, err := service.UpsertGroupLimitConditional(ctx, viewer, in, saved.Revision)
	if err != nil || updated == nil || updated.Revision <= saved.Revision || updated.ApprovalMode != ApprovalModeManual {
		t.Fatalf("conditional own-group update = %+v, %v", updated, err)
	}
	// Silo callers that do not opt into the adapter retain account-wide access.
	vanilla := NewService(base, nil, nil)
	if read, err := vanilla.GetGroupLimit(t.Context(), viewer, groupID); err != nil || read == nil || read.Revision != updated.Revision {
		t.Fatalf("upstream unscoped read = %+v, %v", read, err)
	}
	if _, err := vanilla.UpsertGroupLimitConditional(t.Context(), viewer, in, updated.Revision); err != nil {
		t.Fatalf("upstream unscoped write: %v", err)
	}
}
