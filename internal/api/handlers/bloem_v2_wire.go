package handlers

// Bloem-owned. Bloem's native /api/bloem/v1 routes serve Silo's /api/v2 wire
// shapes directly. Those shapes and their request decoding live in
// internal/apiv2, which imports this package, so apiv2 installs them here
// (internal/apiv2/bloem_v2_wire.go) instead of this package importing it.

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
)

// BloemV2Wire is Silo's /api/v2 projection and request decoding for the
// objects Bloem's native routes share with it. Decoding errors are
// *APIError values carrying the v2 problem's status and identifier.
type BloemV2Wire interface {
	AccessGroup(access.Group) any
	AccessGroupListItem(access.Group) any
	AccessGroupCreate(raw []byte) (access.CreateGroupInput, error)
	AccessGroupUpdate(raw []byte) (access.UpdateGroupInput, error)
	Invitation(*models.Invitation) any
	UserDevices([]AdminUserDeviceView) any
}

var bloemV2 BloemV2Wire

// SetBloemV2Wire installs the v2 wire. internal/apiv2 calls it at init.
func SetBloemV2Wire(wire BloemV2Wire) { bloemV2 = wire }

// bloemV2Available answers 503 when no v2 wire is installed, which happens
// only in a binary that does not link internal/apiv2.
func bloemV2Available(w http.ResponseWriter) bool {
	if bloemV2 == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "The v2 wire is not available")
		return false
	}
	return true
}
