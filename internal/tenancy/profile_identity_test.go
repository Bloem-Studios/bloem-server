package tenancy

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestLegacyProfileIdentityRequiresExactlyOneDefaultMembership(t *testing.T) {
	organizationID := uuid.New()
	groupID := int64(14)
	tests := []struct {
		name       string
		identities []LegacyProfileIdentity
		wantErr    error
	}{
		{name: "none", wantErr: ErrTenantNotFoundOrHidden},
		{name: "multiple", identities: []LegacyProfileIdentity{{OrganizationID: organizationID}, {OrganizationID: uuid.New()}}, wantErr: ErrOwnershipResolutionRequired},
		{name: "one", identities: []LegacyProfileIdentity{{OrganizationID: organizationID, AccessGroupID: &groupID}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &ProfileIdentityResolver{list: func(context.Context, int) ([]LegacyProfileIdentity, error) {
				return tt.identities, nil
			}}
			gotOrganizationID, gotGroupID, err := resolver.ResolveLegacyProfileIdentity(context.Background(), 9)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && (gotOrganizationID != organizationID || gotGroupID == nil || *gotGroupID != groupID) {
				t.Fatalf("identity = %s/%v, want %s/%d", gotOrganizationID, gotGroupID, organizationID, groupID)
			}
		})
	}
}

func TestLegacyProfileIdentityFailsClosedOnSourceError(t *testing.T) {
	sourceErr := errors.New("database failed")
	resolver := &ProfileIdentityResolver{list: func(context.Context, int) ([]LegacyProfileIdentity, error) {
		return nil, sourceErr
	}}
	_, _, err := resolver.ResolveLegacyProfileIdentity(context.Background(), 9)
	if !errors.Is(err, sourceErr) {
		t.Fatalf("error = %v, want wrapped source error", err)
	}
}
