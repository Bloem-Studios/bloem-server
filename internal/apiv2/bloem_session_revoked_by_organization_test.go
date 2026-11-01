package apiv2

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

type revokedByOrganizationViewer struct{}

func (revokedByOrganizationViewer) Resolve(context.Context, access.ResolveInput) (access.Scope, error) {
	return access.Scope{}, apimw.ErrSessionRevokedByOrganization
}

// A profile-scoped operation refused because the session started before its
// organization revoked the account answers session_expired. The web app signs
// out on that answer once its refresh is refused too, which it is, because
// the resolver ended the session. Before, the answer was 404 not_found.
func TestBloemSessionRevokedByOrganizationIsSessionExpired(t *testing.T) {
	deps, _ := catalogDeps(t)
	deps.ViewerAccess = apimw.NewViewerAccessMiddleware(revokedByOrganizationViewer{})
	h := newTestHandler(t, deps)

	rec := do(t, h, "GET", "/api/v2/catalog/items/movie:heat-1995", "", themeSongHeaders())
	requireProblem(t, rec, TypeSessionExpired)
}
