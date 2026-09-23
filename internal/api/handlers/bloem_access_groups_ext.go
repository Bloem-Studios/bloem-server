package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

func accessGroupOrganizationID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	tenant, ok := tenancy.FromContext(r.Context())
	if !ok || tenant.OrganizationID == uuid.Nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return uuid.Nil, false
	}
	return tenant.OrganizationID, true
}
