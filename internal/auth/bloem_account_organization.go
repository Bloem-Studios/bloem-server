package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Only the organization-specific account provisioner supplies this selection.
// It comes from its server-owned transaction input, never request headers or a
// generic tenant context. Identity creation must seed policy in that exact
// organization rather than also creating a default-organization membership.
// The account's default profile follows it there (see
// tenancy.WithCreationOrganization).
func withAccountCreationOrganization(ctx context.Context, id uuid.UUID) context.Context {
	return tenancy.WithCreationOrganization(ctx, id)
}

func accountCreationOrganization(ctx context.Context) *uuid.UUID {
	id, ok := tenancy.CreationOrganization(ctx)
	if !ok {
		return nil
	}
	return &id
}
