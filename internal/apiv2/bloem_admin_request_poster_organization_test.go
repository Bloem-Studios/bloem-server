package apiv2

import (
	"context"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

// organizationPosters refuses r-1 as another organization's request.
type organizationPosters struct{ posteredRequests }

func (organizationPosters) BloemRequireAdminRequestOrganization(_ context.Context, _ mediarequests.Viewer, id string) error {
	if id == "r-1" {
		return mediarequests.ErrForbidden
	}
	return nil
}

// Bloem keeps admin request reads inside the acting organization, so the
// poster of another organization's request is refused before it is fetched.
func TestBloemAdminRequestPosterStaysInItsOrganization(t *testing.T) {
	deps := pilotDeps(nil, nil)
	deps.AdminRequests = organizationPosters{posteredRequests{monitoredRequestQueue{fixtureAdminRequests()}}}
	fetcher := &posterFetcherFake{}
	deps.RemotePosters = fetcher
	users := map[int]*models.User{2: {ID: 2, Role: "admin", Enabled: true}}
	deps.Auth = apimw.NewAuthMiddleware(fakeTokens{}, fakeSessions{}, fakeAPIKeys{keys: map[string]*models.APIKey{
		"sa_monitor": {ID: 1, UserID: 2, Scopes: []string{auth.ScopeAdminMonitorRead}},
	}}, fakeUsers{users: users})
	h := NewHandler(deps)

	requireProblem(t, do(t, h, "GET", Prefix+"/admin/requests/r-1/poster", "", bearer("sa_monitor")), TypePermissionDenied)
	if len(fetcher.urls) != 0 {
		t.Fatalf("fetched another organization's poster: %v", fetcher.urls)
	}
}
