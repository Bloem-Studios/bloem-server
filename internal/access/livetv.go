package access

import (
	"slices"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/permissioncatalog"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// LiveTVAllowed is independent of library grants. Use the resolved permission
// mask, not raw account permissions, so group restrictions remain authoritative.
func LiveTVAllowed(user *models.User, profile *userstore.Profile, effective EffectiveUserPolicy) bool {
	if user == nil {
		return false
	}
	if user.Role == models.RoleAdmin && (profile == nil || profile.IsPrimary) {
		return true
	}
	return slices.Contains(effective.Permissions, permissioncatalog.WatchLiveTV)
}
