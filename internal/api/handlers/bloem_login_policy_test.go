package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type bloemLoginTenantFunc func(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error)

func (f bloemLoginTenantFunc) Resolve(ctx context.Context, id int, org *uuid.UUID, legacy bool) (tenancy.Context, error) {
	return f(ctx, id, org, legacy)
}

type bloemLoginGroupFunc func(context.Context, access.GroupSubject) (*access.GroupPolicy, error)

func (f bloemLoginGroupFunc) ResolvePolicy(ctx context.Context, subject access.GroupSubject) (*access.GroupPolicy, error) {
	return f(ctx, subject)
}

func TestBloemLoginPolicyUsesAuthenticatedAccountTenant(t *testing.T) {
	groupID := int64(9)
	user := &models.User{ID: 7, Role: models.RoleUser, AccessGroupID: &groupID}
	valid := tenancy.Context{AccountID: user.ID, OrganizationID: uuid.New(), MembershipID: uuid.New(), Legacy: true, OrganizationDefault: true, OrganizationStatus: tenancy.OrganizationActive, MembershipStatus: tenancy.MembershipActive, PolicyRevision: 1, SecurityRevision: 1}
	for _, tc := range []struct {
		name                     string
		change                   func(*tenancy.Context)
		resolveErr, groupErr     error
		allowed, want, wantGroup bool
	}{
		{name: "group denies", wantGroup: true},
		{name: "group permits", allowed: true, want: true, wantGroup: true},
		{name: "group lookup failure", groupErr: errors.New("unavailable"), wantGroup: true},
		{name: "tenant unavailable", resolveErr: tenancy.ErrTenantUnavailable},
		{name: "tenant suspended", resolveErr: tenancy.ErrTenantSuspended},
		{name: "wrong account", change: func(c *tenancy.Context) { c.AccountID++ }},
		{name: "missing organization", change: func(c *tenancy.Context) { c.OrganizationID = uuid.Nil }},
		{name: "missing membership", change: func(c *tenancy.Context) { c.MembershipID = uuid.Nil }},
		{name: "foreign organization", change: func(c *tenancy.Context) { c.OrganizationDefault = false }},
		{name: "nonlegacy selection", change: func(c *tenancy.Context) { c.Legacy = false }},
		{name: "membership suspended", change: func(c *tenancy.Context) { c.MembershipStatus = tenancy.MembershipSuspended }},
		{name: "organization suspended", change: func(c *tenancy.Context) { c.OrganizationStatus = tenancy.OrganizationSuspended }},
		{name: "missing revision", change: func(c *tenancy.Context) { c.SecurityRevision = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant := valid
			if tc.change != nil {
				tc.change(&tenant)
			}
			h := &AuthHandler{}
			h.SetLoginTenantResolver(bloemLoginTenantFunc(func(ctx context.Context, id int, org *uuid.UUID, legacy bool) (tenancy.Context, error) {
				if id != user.ID || org != nil || !legacy {
					t.Fatal("login used caller-selected authority")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded tenant resolution")
				}
				return tenant, tc.resolveErr
			}))
			groupCalled := false
			h.SetAccessGroupProvider(bloemLoginGroupFunc(func(_ context.Context, subject access.GroupSubject) (*access.GroupPolicy, error) {
				groupCalled = true
				if subject.AccountID != user.ID || subject.OrganizationID != valid.OrganizationID || subject.ProfileID != "" || !subject.Legacy {
					t.Fatal("wrong policy subject")
				}
				return &access.GroupPolicy{ID: groupID, DownloadAllowed: tc.allowed}, tc.groupErr
			}))
			foreign := tenancy.WithContext(t.Context(), tenancy.Context{AccountID: 123, OrganizationID: uuid.New()})
			if got := h.loginDownloadAllowed(foreign, user); got != tc.want {
				t.Fatalf("download_allowed = %v, want %v", got, tc.want)
			}
			if groupCalled != tc.wantGroup {
				t.Fatalf("group lookup = %v, want %v", groupCalled, tc.wantGroup)
			}
		})
	}
}
