package handlers

import (
	"errors"

	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

// EnableTenantRequestPolicy installs on the service shared with the v2
// listener, retaining the administrative group-limit decorator when present.
func (h *RequestsHandler) EnableTenantRequestPolicy() error {
	switch service := h.service.(type) {
	case *mediarequests.Service:
		return service.EnableTenantRequestPolicy()
	case *mediarequests.TenantGroupLimitService:
		return service.Service.EnableTenantRequestPolicy()
	default:
		return errors.New("tenant request policy requires the request service")
	}
}
