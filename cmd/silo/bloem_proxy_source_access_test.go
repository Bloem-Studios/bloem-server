package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/proxy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type proxySourceFilesStub map[int]*models.MediaFile

func (s proxySourceFilesStub) GetByID(_ context.Context, id int) (*models.MediaFile, error) {
	if file, ok := s[id]; ok {
		return file, nil
	}
	return nil, errors.New("no rows")
}

// proxySourceTenantStoreStub holds organizations and memberships the way
// tenancy.Store does: an account has at most one membership.
type proxySourceTenantStoreStub struct {
	organizations map[uuid.UUID]tenancy.Organization
	memberships   map[int]tenancy.Membership
}

func (s proxySourceTenantStoreStub) DefaultOrganization(context.Context) (tenancy.Organization, error) {
	for _, organization := range s.organizations {
		if organization.Default {
			return organization, nil
		}
	}
	return tenancy.Organization{}, tenancy.ErrOwnershipResolutionRequired
}

func (s proxySourceTenantStoreStub) GetOrganization(_ context.Context, id uuid.UUID) (tenancy.Organization, error) {
	if organization, ok := s.organizations[id]; ok {
		return organization, nil
	}
	return tenancy.Organization{}, tenancy.ErrOrganizationNotFound
}

func (s proxySourceTenantStoreStub) GetMembership(_ context.Context, accountID int, organizationID uuid.UUID) (tenancy.Membership, error) {
	if membership, ok := s.memberships[accountID]; ok && membership.OrganizationID == organizationID {
		return membership, nil
	}
	return tenancy.Membership{}, tenancy.ErrMembershipNotFound
}

func (s proxySourceTenantStoreStub) ProfileOrganization(context.Context, int, string) (uuid.UUID, error) {
	return uuid.Nil, tenancy.ErrTenantNotFoundOrHidden
}

func (s proxySourceTenantStoreStub) AccountOrganization(_ context.Context, accountID int) (uuid.UUID, error) {
	if membership, ok := s.memberships[accountID]; ok && membership.Status == tenancy.MembershipActive {
		return membership.OrganizationID, nil
	}
	return uuid.Nil, tenancy.ErrMembershipNotFound
}

// proxySourceResourcesStub entitles each media folder to one organization.
type proxySourceResourcesStub map[int64]uuid.UUID

func (s proxySourceResourcesStub) RequireAccess(_ context.Context, tenant tenancy.Context, root resourcetenancy.RootRef) (resourcetenancy.Grant, error) {
	if root.Kind == resourcetenancy.RootMediaFolder && s[root.ID] == tenant.OrganizationID {
		return resourcetenancy.Grant{}, nil
	}
	return resourcetenancy.Grant{}, resourcetenancy.ErrResourceHidden
}

// A stream token carries an account but no organization, so the proxy must
// find the organization the account belongs to. Resolving every account
// through the deployment default organization hid a hosted organization's
// own libraries from its own members.
func TestProxySourceAccessResolvesTheAccountsOwnOrganization(t *testing.T) {
	ctx := context.Background()
	defaultOrganization := tenancy.Organization{ID: uuid.New(), Status: tenancy.OrganizationActive, Default: true}
	hostOwner := 30
	hostedOrganization := tenancy.Organization{ID: uuid.New(), Status: tenancy.OrganizationActive, OwnerAccountID: &hostOwner}
	const (
		defaultMember   = 10
		hostedMember    = 20
		suspendedMember = 21
		noMembership    = 40
		defaultFolder   = 1
		hostedFolder    = 2
		defaultFile     = 100
		hostedFile      = 200
	)
	member := func(accountID int, organization tenancy.Organization, status tenancy.MembershipStatus) tenancy.Membership {
		return tenancy.Membership{ID: uuid.New(), OrganizationID: organization.ID, AccountID: accountID, Status: status, SecurityRevision: 1}
	}
	access := buildProxySourceAccess(
		proxySourceFilesStub{
			defaultFile: {ID: defaultFile, MediaFolderID: defaultFolder, FilePath: "/mnt/media/default.mkv"},
			hostedFile:  {ID: hostedFile, MediaFolderID: hostedFolder, FilePath: "/mnt/media/hosted.mkv"},
		},
		proxySourceTenantStoreStub{
			organizations: map[uuid.UUID]tenancy.Organization{
				defaultOrganization.ID: defaultOrganization,
				hostedOrganization.ID:  hostedOrganization,
			},
			memberships: map[int]tenancy.Membership{
				defaultMember:   member(defaultMember, defaultOrganization, tenancy.MembershipActive),
				hostedMember:    member(hostedMember, hostedOrganization, tenancy.MembershipActive),
				suspendedMember: member(suspendedMember, hostedOrganization, tenancy.MembershipSuspended),
			},
		},
		proxySourceResourcesStub{defaultFolder: defaultOrganization.ID, hostedFolder: hostedOrganization.ID},
	)

	for _, tc := range []struct {
		name    string
		account int
		file    int
		want    error
	}{
		{name: "hosted member reads its organization's library", account: hostedMember, file: hostedFile},
		{name: "default member reads the default library", account: defaultMember, file: defaultFile},
		{name: "hosted member cannot read the default library", account: hostedMember, file: defaultFile, want: proxy.ErrSourceHidden},
		{name: "default member cannot read a hosted library", account: defaultMember, file: hostedFile, want: proxy.ErrSourceHidden},
		{name: "suspended hosted member is hidden", account: suspendedMember, file: hostedFile, want: proxy.ErrSourceHidden},
		{name: "account without a membership is hidden", account: noMembership, file: defaultFile, want: proxy.ErrSourceHidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := access.AllowMediaSource(ctx, tc.account, tc.file)
			if tc.want == nil && err != nil {
				t.Fatalf("AllowMediaSource(account %d, file %d) = %v; want nil", tc.account, tc.file, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("AllowMediaSource(account %d, file %d) = %v; want %v", tc.account, tc.file, err, tc.want)
			}
		})
	}
}
