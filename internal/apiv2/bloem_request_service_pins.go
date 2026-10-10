package apiv2

import (
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

// The service shared by both listeners must retain the request interfaces
// used by optional native operations when Bloem overrides global routing.
var (
	_ handlers.RequestService  = (*mediarequests.TenantGroupLimitService)(nil)
	_ adminRequestRoutes       = (*mediarequests.TenantGroupLimitService)(nil)
	_ guardedAdminRequests     = (*mediarequests.TenantGroupLimitService)(nil)
	_ adminRequestQueue        = (*mediarequests.TenantGroupLimitService)(nil)
	_ adminRequestCloser       = (*mediarequests.TenantGroupLimitService)(nil)
	_ adminRequestGroups       = (*mediarequests.TenantGroupLimitService)(nil)
	_ WatchlistRequestService  = (*mediarequests.TenantGroupLimitService)(nil)
	_ AdminRequestUsageService = (*mediarequests.TenantGroupLimitService)(nil)
	_ RequestLifecycleService  = (*mediarequests.TenantGroupLimitService)(nil)
)
