package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func adminGroupOrganization(ctx context.Context) (uuid.UUID, error) {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || tenant.OrganizationID == uuid.Nil {
		return uuid.Nil, apiError(503, "tenant_unavailable", "Tenant authorization is unavailable")
	}
	return tenant.OrganizationID, nil
}
