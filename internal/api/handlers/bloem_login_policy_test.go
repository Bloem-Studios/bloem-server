package handlers

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
)

// Password login reports the signed-in account's own group policy. The login
// request carries no tenant, so without Bloem's binding Silo's
// effectiveAccountPolicy falls back to the permissive no-group policy and the
// response advertises downloads and permissions the account's group denies.
func TestBloemLoginReportsTheAccountGroupPolicyDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	pool := newInvitationTestDatabase(t, ctx)
	users := auth.NewUserRepository(pool)
	account, err := users.Create(ctx, models.CreateUserInput{
		Username: "grouped", Email: "grouped@example.test", Password: "grouped-test-password",
		Role: models.RoleUser, Permissions: []string{"marker_edit", "metadata_curation"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var groupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO access_groups (organization_id,name,download_allowed,allowed_permissions)
SELECT id,'No downloads',false,ARRAY['metadata_curation'] FROM organizations WHERE is_default RETURNING id`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET access_group_id=$1 WHERE id=$2`, groupID, account.ID); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionRepository(pool)
	jwt := auth.NewJWTService("login-policy-test-secret", time.Hour, 24*time.Hour)
	service := auth.NewService(auth.NewLocalProvider(users, sessions), jwt, sessions, users, auth.NewInviteCodeRepository(pool), nil, pgstore.NewPostgresProvider(pool))
	h := NewAuthHandler(service, jwt, nil)
	h.SetLoginTenantResolver(tenancy.NewResolver(tenancy.NewStore(pool)))
	h.SetAccessGroupProvider(access.NewTenantGroupStore(pool))

	view, err := h.Login(ctx, LoginInput{Username: "grouped", Password: "grouped-test-password", DeviceName: "login-policy-test"})
	if err != nil {
		t.Fatal(err)
	}
	if view.User.DownloadAllowed {
		t.Fatal("login reports download_allowed although the account's group denies downloads")
	}
	if want := []string{"metadata_curation"}; !slices.Equal(view.User.PolicyPermissions, want) {
		t.Fatalf("login permissions = %v, want the group-masked %v", view.User.PolicyPermissions, want)
	}
}

type bloemLoginTenantFunc func(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error)

func (f bloemLoginTenantFunc) Resolve(ctx context.Context, id int, org *uuid.UUID, legacy bool) (tenancy.Context, error) {
	return f(ctx, id, org, legacy)
}

type bloemLoginGroupFunc func(context.Context, access.GroupSubject) (*access.GroupPolicy, error)

func (f bloemLoginGroupFunc) GetPolicyForUser(ctx context.Context, userID int) (*access.GroupPolicy, error) {
	return f.ResolvePolicy(ctx, access.GroupSubject{AccountID: userID})
}

func (f bloemLoginGroupFunc) ResolvePolicy(ctx context.Context, subject access.GroupSubject) (*access.GroupPolicy, error) {
	return f(ctx, subject)
}

func TestBloemLoginPolicyUsesAuthenticatedAccountTenant(t *testing.T) {
	groupID := int64(9)
	user := &models.User{ID: 7, Role: models.RoleUser, Enabled: true, Permissions: []string{"marker_edit", "metadata_curation"}, AccessGroupID: &groupID}
	valid := tenancy.Context{AccountID: user.ID, OrganizationID: uuid.New(), MembershipID: uuid.New(), Legacy: true, OrganizationDefault: true, OrganizationStatus: tenancy.OrganizationActive, MembershipStatus: tenancy.MembershipActive, PolicyRevision: 1, SecurityRevision: 1}
	masked := []string{"metadata_curation"}
	closed := accountPolicy{Permissions: []string{}}
	for _, tc := range []struct {
		name                 string
		change               func(*tenancy.Context)
		resolveErr, groupErr error
		allowed, wantGroup   bool
		want                 accountPolicy
	}{
		{name: "group denies", wantGroup: true, want: accountPolicy{Permissions: masked}},
		{name: "group permits", allowed: true, wantGroup: true, want: accountPolicy{DownloadAllowed: true, Permissions: masked}},
		{name: "group lookup failure", groupErr: errors.New("unavailable"), wantGroup: true, want: closed},
		{name: "tenant unavailable", resolveErr: tenancy.ErrTenantUnavailable, want: closed},
		{name: "tenant suspended", resolveErr: tenancy.ErrTenantSuspended, want: closed},
		{name: "wrong account", change: func(c *tenancy.Context) { c.AccountID++ }, want: closed},
		{name: "missing organization", change: func(c *tenancy.Context) { c.OrganizationID = uuid.Nil }, want: closed},
		{name: "missing membership", change: func(c *tenancy.Context) { c.MembershipID = uuid.Nil }, want: closed},
		{name: "foreign organization", change: func(c *tenancy.Context) { c.OrganizationDefault = false }, want: closed},
		{name: "nonlegacy selection", change: func(c *tenancy.Context) { c.Legacy = false }, want: closed},
		{name: "membership suspended", change: func(c *tenancy.Context) { c.MembershipStatus = tenancy.MembershipSuspended }, want: closed},
		{name: "organization suspended", change: func(c *tenancy.Context) { c.OrganizationStatus = tenancy.OrganizationSuspended }, want: closed},
		{name: "missing revision", change: func(c *tenancy.Context) { c.SecurityRevision = 0 }, want: closed},
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
			h.SetAccessGroupProvider(bloemLoginGroupFunc(func(ctx context.Context, subject access.GroupSubject) (*access.GroupPolicy, error) {
				groupCalled = true
				if subject.AccountID != user.ID || subject.OrganizationID != valid.OrganizationID || subject.ProfileID != "" || !subject.Legacy {
					t.Fatal("wrong policy subject")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded group lookup")
				}
				return &access.GroupPolicy{ID: groupID, DownloadAllowed: tc.allowed, AllowedPermissions: masked}, tc.groupErr
			}))
			// The request's own tenant, even one naming this account, is not
			// authority for the account login has just authenticated.
			ambient := tenancy.WithContext(t.Context(), tenancy.Context{AccountID: user.ID, OrganizationID: uuid.New(), Legacy: true})
			ctx, done := h.bloemLoginPolicyContext(ambient, user)
			defer done()
			if got := effectiveAccountPolicy(ctx, user, h.accessGroups); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("account policy = %+v, want %+v", got, tc.want)
			}
			if groupCalled != tc.wantGroup {
				t.Fatalf("group lookup = %v, want %v", groupCalled, tc.wantGroup)
			}
		})
	}
}
