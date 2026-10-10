package requests

import "context"

// Request routes and the routing mode are server-global, like request
// integrations. The installed Bloem service decorator applies their existing
// platform-authority boundary while leaving upstream administration and
// background fulfillment unchanged. TMDB title search exposes no global
// configuration and keeps its promoted implementation.
func (s *TenantGroupLimitService) ListRoutesAdmin(ctx context.Context, viewer Viewer) ([]Route, error) {
	if !viewer.IsAdmin {
		return nil, ErrForbidden
	}
	if err := s.requirePlatformAuthority(ctx, viewer); err != nil {
		return nil, err
	}
	return s.Service.ListRoutesAdmin(ctx, viewer)
}

func (s *TenantGroupLimitService) GetRoute(ctx context.Context, viewer Viewer, id string) (*Route, error) {
	if !viewer.IsAdmin {
		return nil, ErrForbidden
	}
	if err := s.requirePlatformAuthority(ctx, viewer); err != nil {
		return nil, err
	}
	return s.Service.GetRoute(ctx, viewer, id)
}

func (s *TenantGroupLimitService) CreateRoute(ctx context.Context, viewer Viewer, route Route) (*Route, error) {
	if !viewer.IsAdmin {
		return nil, ErrForbidden
	}
	if err := s.requirePlatformAuthority(ctx, viewer); err != nil {
		return nil, err
	}
	return s.Service.CreateRoute(ctx, viewer, route)
}

func (s *TenantGroupLimitService) UpdateRouteConditional(ctx context.Context, viewer Viewer, route Route, expected int64) (*Route, error) {
	if !viewer.IsAdmin {
		return nil, ErrForbidden
	}
	if err := s.requirePlatformAuthority(ctx, viewer); err != nil {
		return nil, err
	}
	return s.Service.UpdateRouteConditional(ctx, viewer, route, expected)
}

func (s *TenantGroupLimitService) DeleteRouteConditional(ctx context.Context, viewer Viewer, id string, expected int64) error {
	if !viewer.IsAdmin {
		return ErrForbidden
	}
	if err := s.requirePlatformAuthority(ctx, viewer); err != nil {
		return err
	}
	return s.Service.DeleteRouteConditional(ctx, viewer, id, expected)
}

func (s *TenantGroupLimitService) ReorderRoutes(ctx context.Context, viewer Viewer, mediaType MediaType, ids []string) ([]Route, error) {
	if !viewer.IsAdmin {
		return nil, ErrForbidden
	}
	if err := s.requirePlatformAuthority(ctx, viewer); err != nil {
		return nil, err
	}
	return s.Service.ReorderRoutes(ctx, viewer, mediaType, ids)
}

func (s *TenantGroupLimitService) PreviewRoute(ctx context.Context, viewer Viewer, mediaType MediaType, tmdbID, requesterUserID int) (*RoutePreview, error) {
	if !viewer.IsAdmin {
		return nil, ErrForbidden
	}
	if err := s.requirePlatformAuthority(ctx, viewer); err != nil {
		return nil, err
	}
	return s.Service.PreviewRoute(ctx, viewer, mediaType, tmdbID, requesterUserID)
}

func (s *TenantGroupLimitService) GetRoutingOverview(ctx context.Context, viewer Viewer) (*RoutingOverview, error) {
	if !viewer.IsAdmin {
		return nil, ErrForbidden
	}
	if err := s.requirePlatformAuthority(ctx, viewer); err != nil {
		return nil, err
	}
	return s.Service.GetRoutingOverview(ctx, viewer)
}

func (s *TenantGroupLimitService) UpdateRoutingModeConditional(ctx context.Context, viewer Viewer, mode RoutingMode, expected int64) (*RoutingOverview, error) {
	if !viewer.IsAdmin {
		return nil, ErrForbidden
	}
	if err := s.requirePlatformAuthority(ctx, viewer); err != nil {
		return nil, err
	}
	return s.Service.UpdateRoutingModeConditional(ctx, viewer, mode, expected)
}
