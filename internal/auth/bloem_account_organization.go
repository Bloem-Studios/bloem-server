package auth

import (
	"context"

	"github.com/google/uuid"
)

type accountCreationOrganizationKey struct{}

// Only the organization-specific account provisioner supplies this selection.
// It comes from its server-owned transaction input, never request headers or a
// generic tenant context. Identity creation must seed policy in that exact
// organization rather than also creating a default-organization membership.
func withAccountCreationOrganization(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, accountCreationOrganizationKey{}, id)
}

func accountCreationOrganization(ctx context.Context) *uuid.UUID {
	id, ok := ctx.Value(accountCreationOrganizationKey{}).(uuid.UUID)
	if !ok {
		return nil
	}
	return &id
}
