package handlers

import (
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// bloemAuthHandlerExt holds the Bloem-only AuthHandler dependencies so the
// Silo-owned struct carries a single embedded line.
type bloemAuthHandlerExt struct {
	loginTenants apimw.TenantResolver
}
