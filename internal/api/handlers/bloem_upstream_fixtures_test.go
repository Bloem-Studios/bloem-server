package handlers

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"
	"net/http"
	"net/http/httptest"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

type fakeRequestService struct {
	listStudiosFn  func() ([]mediarequests.DiscoverBrandCard, error)
	listNetworksFn func() ([]mediarequests.DiscoverBrandCard, error)
	listGenresFn   func() ([]mediarequests.DiscoverBrandCard, error)
	browseFn       func(kind, slug string, mediaType mediarequests.MediaType, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error)
	loadOptionsErr error
}

func (f *fakeRequestService) ListStudios(context.Context, mediarequests.Viewer) ([]mediarequests.DiscoverBrandCard, error) {
	if f.listStudiosFn != nil {
		return f.listStudiosFn()
	}
	return nil, nil
}

func (f *fakeRequestService) ListNetworks(context.Context, mediarequests.Viewer) ([]mediarequests.DiscoverBrandCard, error) {
	if f.listNetworksFn != nil {
		return f.listNetworksFn()
	}
	return nil, nil
}

func (f *fakeRequestService) ListGenres(context.Context, mediarequests.Viewer) ([]mediarequests.DiscoverBrandCard, error) {
	if f.listGenresFn != nil {
		return f.listGenresFn()
	}
	return nil, nil
}

func (f *fakeRequestService) BrowseStudio(_ context.Context, _ mediarequests.Viewer, slug, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error) {
	return f.browseFn("studio", slug, mediarequests.MediaTypeMovie, sort, page)
}

func (f *fakeRequestService) BrowseNetwork(_ context.Context, _ mediarequests.Viewer, slug, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error) {
	return f.browseFn("network", slug, mediarequests.MediaTypeSeries, sort, page)
}

func (f *fakeRequestService) BrowseGenre(_ context.Context, _ mediarequests.Viewer, slug string, mediaType mediarequests.MediaType, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error) {
	return f.browseFn("genre", slug, mediaType, sort, page)
}

func (f *fakeRequestService) Follow(context.Context, mediarequests.Viewer, mediarequests.MediaType, int) (mediarequests.RequestState, error) {
	return mediarequests.RequestState{}, nil
}

func (f *fakeRequestService) Unfollow(context.Context, mediarequests.Viewer, mediarequests.MediaType, int) error {
	return nil
}

func (f *fakeRequestService) Search(context.Context, mediarequests.Viewer, string, mediarequests.MediaType, int) (*mediarequests.MediaPage, error) {
	return nil, nil
}

func (f *fakeRequestService) Discover(context.Context, mediarequests.Viewer, string, int) (*mediarequests.DiscoverySection, error) {
	return nil, nil
}

func (f *fakeRequestService) DiscoverAll(context.Context, mediarequests.Viewer) ([]mediarequests.DiscoverySection, error) {
	return nil, nil
}

func (f *fakeRequestService) GetDetail(context.Context, mediarequests.Viewer, mediarequests.MediaType, int) (*mediarequests.MediaDetail, error) {
	return nil, nil
}

func (f *fakeRequestService) CreateRequest(context.Context, mediarequests.Viewer, mediarequests.CreateRequestInput) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) ListMine(context.Context, mediarequests.Viewer, mediarequests.ListFilter) ([]*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) ListAdmin(context.Context, mediarequests.Viewer, mediarequests.ListFilter) ([]*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) GetRequest(context.Context, mediarequests.Viewer, string) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) Approve(context.Context, mediarequests.Viewer, string) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) Decline(context.Context, mediarequests.Viewer, string, string) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) Cancel(context.Context, mediarequests.Viewer, string, string) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) Retry(context.Context, mediarequests.Viewer, string) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) RequestCapabilityAllowed(context.Context, mediarequests.Viewer) (bool, error) {
	return true, nil
}

func (f *fakeRequestService) GetFeatureStatus(context.Context, mediarequests.Viewer) (mediarequests.FeatureStatus, error) {
	return mediarequests.FeatureStatus{}, nil
}

func (f *fakeRequestService) GetSettings(context.Context, mediarequests.Viewer) (mediarequests.Settings, error) {
	return mediarequests.Settings{}, nil
}

func (f *fakeRequestService) UpdateSettings(context.Context, mediarequests.Viewer, mediarequests.Settings) (mediarequests.Settings, error) {
	return mediarequests.Settings{}, nil
}

func (f *fakeRequestService) GetUserLimit(context.Context, mediarequests.Viewer, int) (*mediarequests.UserLimit, error) {
	return nil, nil
}

func (f *fakeRequestService) UpsertUserLimit(context.Context, mediarequests.Viewer, mediarequests.UserLimit) (*mediarequests.UserLimit, error) {
	return nil, nil
}

func (f *fakeRequestService) ListIntegrations(context.Context, mediarequests.Viewer) ([]mediarequests.Integration, error) {
	return nil, nil
}

func (f *fakeRequestService) CreateIntegration(_ context.Context, _ mediarequests.Viewer, integration mediarequests.Integration) (*mediarequests.Integration, error) {
	return &integration, nil
}

func (f *fakeRequestService) UpdateIntegration(_ context.Context, _ mediarequests.Viewer, integration mediarequests.Integration) (*mediarequests.Integration, error) {
	return &integration, nil
}

func (f *fakeRequestService) DeleteIntegration(context.Context, mediarequests.Viewer, string) error {
	return nil
}

func (f *fakeRequestService) LoadIntegrationOptions(context.Context, mediarequests.Viewer, mediarequests.Integration) (map[string][]mediarequests.RouterOption, error) {
	return nil, f.loadOptionsErr
}

func authedRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	ctx := apimw.SetClaims(req.Context(), &auth.Claims{
		UserID:    1,
		Role:      "user",
		TokenType: auth.TokenTypeAccess,
	})
	ctx = apimw.SetProfileID(ctx, "profile-1")
	return req.WithContext(ctx)
}
