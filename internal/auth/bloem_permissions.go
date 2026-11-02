package auth

import "github.com/Silo-Server/silo-server/internal/permissioncatalog"

// Bloem's permission catalog adds Live TV (watch_live_tv) to Silo's assignable
// permissions. Registering the whole catalog in Silo's map keeps one registry:
// NormalizePermissions accepts what the catalog names, and
// AssignablePermissions (the frozen v1 admin list) names all of it.
func init() {
	for _, permission := range permissioncatalog.Assignable() {
		assignablePermissions[Permission(permission)] = struct{}{}
	}
}
