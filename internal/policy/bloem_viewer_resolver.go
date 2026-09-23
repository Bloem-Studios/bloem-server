package policy

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// TenantLibraryResolver resolves the media folders visible to an authoritative
// tenant context.
type TenantLibraryResolver interface {
	AvailableMediaFolderIDs(context.Context, tenancy.Context) ([]int, error)
}
