package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type nativeTenantStub struct{ tenant tenancy.Context }

func (s nativeTenantStub) Resolve(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error) {
	return s.tenant, nil
}

type nativeResourcesStub struct {
	owner     uuid.UUID
	denied    resourcetenancy.RootKind
	deniedErr error
	calls     []resourcetenancy.RootRef
}

func (s *nativeResourcesStub) RequireAccess(_ context.Context, _ tenancy.Context, root resourcetenancy.RootRef) (resourcetenancy.Grant, error) {
	s.calls = append(s.calls, root)
	if root.Kind == s.denied {
		if s.deniedErr != nil {
			return resourcetenancy.Grant{}, s.deniedErr
		}
		return resourcetenancy.Grant{}, resourcetenancy.ErrResourceHidden
	}
	return resourcetenancy.Grant{Root: root, Owner: resourcetenancy.Owner{ID: s.owner}}, nil
}
func TestNativeSourceRequiresFreshMembershipAndBothResourceGrants(t *testing.T) {
	for _, kind := range []string{"allowed", "no-tenant", "stale-membership", "folder-denied", "installation-denied", "owner-changed"} {
		t.Run(kind, func(t *testing.T) {
			tenant := tenancy.Context{OrganizationID: uuid.New(), MembershipID: uuid.New(), AccountID: 7, PolicyRevision: 1, SecurityRevision: 1}
			ctx := tenancy.WithContext(context.Background(), tenant)
			owner := uuid.New()
			id := int64(13)
			source := storagesource.SourceConfig{OwnerID: owner, InstallationID: &id}
			resolver := nativeTenantStub{tenant: tenant}
			resources := &nativeResourcesStub{owner: owner}
			switch kind {
			case "no-tenant":
				ctx = context.Background()
			case "stale-membership":
				resolver.tenant.SecurityRevision++
			case "folder-denied":
				resources.denied = resourcetenancy.RootMediaFolder
			case "installation-denied":
				resources.denied = resourcetenancy.RootPluginInstallation
			case "owner-changed":
				resources.owner = uuid.New()
			}
			err := authorizeNativeStorageSource(ctx, &models.MediaFile{MediaFolderID: 9}, source, resolver, resources)
			if (err == nil) != (kind == "allowed") {
				t.Fatalf("result=%v", err)
			}
			if kind == "allowed" {
				if len(resources.calls) != 2 || resources.calls[0] != (resourcetenancy.RootRef{Kind: resourcetenancy.RootMediaFolder, ID: 9}) || resources.calls[1] != (resourcetenancy.RootRef{Kind: resourcetenancy.RootPluginInstallation, ID: 13}) {
					t.Fatalf("policy calls=%v", resources.calls)
				}
			}
		})
	}
}

// Both resource-policy boundaries must translate wrapped hidden-resource errors
// into authorization denials while preserving unrelated policy failures.
func TestNativeSourceResourceErrors(t *testing.T) {
	for _, root := range []resourcetenancy.RootKind{resourcetenancy.RootMediaFolder, resourcetenancy.RootPluginInstallation} {
		t.Run(string(root), func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				err      error
				wantCode codes.Code
			}{
				{"direct-hidden", resourcetenancy.ErrResourceHidden, codes.PermissionDenied},
				{"wrapped-hidden", fmt.Errorf("resource policy: %w", resourcetenancy.ErrResourceHidden), codes.PermissionDenied},
				{"other-policy-error", status.Error(codes.Unavailable, "resource policy unavailable"), codes.Unavailable},
			} {
				t.Run(tc.name, func(t *testing.T) {
					tenant := tenancy.Context{OrganizationID: uuid.New(), MembershipID: uuid.New(), AccountID: 7, PolicyRevision: 1, SecurityRevision: 1}
					owner := uuid.New()
					installationID := int64(13)
					resources := &nativeResourcesStub{owner: owner, denied: root, deniedErr: tc.err}
					err := authorizeNativeStorageSource(tenancy.WithContext(context.Background(), tenant), &models.MediaFile{MediaFolderID: 9}, storagesource.SourceConfig{OwnerID: owner, InstallationID: &installationID}, nativeTenantStub{tenant: tenant}, resources)
					if got := status.Code(err); got != tc.wantCode {
						t.Fatalf("authorization code=%v, want %v (error=%v)", got, tc.wantCode, err)
					}
					if tc.wantCode == codes.Unavailable && !errors.Is(err, tc.err) {
						t.Fatalf("unrelated policy error lost: %v", err)
					}
				})
			}
		})
	}
}
