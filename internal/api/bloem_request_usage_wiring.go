package api

import (
	"fmt"

	"github.com/Silo-Server/silo-server/internal/apiv2"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

// The admin endpoint explicitly targets an account. Its adapter checks current
// tenant authority without changing the shared viewer service's profile policy.
func wireBloemAdminRequestUsage(v2deps *apiv2.Dependencies) {
	var service *mediarequests.Service
	switch usage := v2deps.AdminRequestUsage.(type) {
	case *mediarequests.Service:
		service = usage
	case *mediarequests.TenantGroupLimitService:
		service = usage.Service
	default:
		return
	}
	usage, err := mediarequests.NewTenantAdminRequestUsageService(service)
	if err != nil {
		panic(fmt.Sprintf("install Bloem admin request usage: %v", err))
	}
	v2deps.AdminRequestUsage = usage
}
