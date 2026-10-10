package handlers

import (
	"errors"

	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

// EnableTenantGroupLimits installs Bloem's group-limit boundary before either
// listener consumes Service. It is independent of lifecycle idempotency and
// must also be installed when the JWT secret is empty.
func (h *RequestsHandler) EnableTenantGroupLimits() error {
	switch service := h.service.(type) {
	case *mediarequests.TenantGroupLimitService:
		return nil
	case *mediarequests.Service:
		bounded, err := mediarequests.NewTenantGroupLimitService(service)
		if err != nil {
			return err
		}
		h.service = bounded
		return nil
	default:
		return errors.New("tenant request group limits require the request service")
	}
}
