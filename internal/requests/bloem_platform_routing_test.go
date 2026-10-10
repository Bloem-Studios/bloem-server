package requests

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/metadata/tmdb"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// Every global administration entry point must deny before consulting storage,
// validating input or contacting TMDB. HTTP CRUD success is covered by the
// production-composition test in internal/api.
var platformRoutingCalls = map[string]func(context.Context, *TenantGroupLimitService, Viewer) error{
	"ListRoutesAdmin": func(ctx context.Context, s *TenantGroupLimitService, v Viewer) error {
		_, err := s.ListRoutesAdmin(ctx, v)
		return err
	},
	"GetRoute": func(ctx context.Context, s *TenantGroupLimitService, v Viewer) error {
		_, err := s.GetRoute(ctx, v, "route")
		return err
	},
	"CreateRoute": func(ctx context.Context, s *TenantGroupLimitService, v Viewer) error {
		_, err := s.CreateRoute(ctx, v, Route{})
		return err
	},
	"UpdateRouteConditional": func(ctx context.Context, s *TenantGroupLimitService, v Viewer) error {
		_, err := s.UpdateRouteConditional(ctx, v, Route{ID: "route"}, 1)
		return err
	},
	"DeleteRouteConditional": func(ctx context.Context, s *TenantGroupLimitService, v Viewer) error {
		return s.DeleteRouteConditional(ctx, v, "route", 1)
	},
	"ReorderRoutes": func(ctx context.Context, s *TenantGroupLimitService, v Viewer) error {
		_, err := s.ReorderRoutes(ctx, v, MediaTypeMovie, []string{"route"})
		return err
	},
	"PreviewRoute": func(ctx context.Context, s *TenantGroupLimitService, v Viewer) error {
		_, err := s.PreviewRoute(ctx, v, MediaTypeMovie, 550, 0)
		return err
	},
	"GetRoutingOverview": func(ctx context.Context, s *TenantGroupLimitService, v Viewer) error {
		_, err := s.GetRoutingOverview(ctx, v)
		return err
	},
	"UpdateRoutingModeConditional": func(ctx context.Context, s *TenantGroupLimitService, v Viewer) error {
		_, err := s.UpdateRoutingModeConditional(ctx, v, RoutingAdvanced, 1)
		return err
	},
}

func TestBloemPlatformRoutingAuthority(t *testing.T) {
	for _, boundary := range []struct {
		name   string
		ctx    context.Context
		scope  TenantScopeResolver
		viewer Viewer
	}{
		{
			name: "hosted_primary_membership",
			ctx:  t.Context(),
			scope: &platformScope{
				fakeTenantScope: &fakeTenantScope{orgs: map[int]uuid.UUID{1: orgBeta}},
				defaultOrg:      orgAlpha,
			},
			viewer: adminViewer(1),
		},
		{
			name: "hosted_context_overrides_default_primary_membership",
			ctx:  tenancy.WithContext(t.Context(), tenancy.Context{AccountID: 1, OrganizationID: orgBeta}),
			scope: &platformScope{
				fakeTenantScope: &fakeTenantScope{orgs: map[int]uuid.UUID{1: orgAlpha}},
				defaultOrg:      orgAlpha,
			},
			viewer: adminViewer(1),
		},
		{
			name:   "foreign_account_context",
			ctx:    tenancy.WithContext(t.Context(), tenancy.Context{AccountID: 99, OrganizationID: orgAlpha}),
			scope:  &platformScope{defaultOrg: orgAlpha},
			viewer: adminViewer(1),
		},
		{
			name:   "empty_organization_context",
			ctx:    tenancy.WithContext(t.Context(), tenancy.Context{AccountID: 1}),
			scope:  &platformScope{defaultOrg: orgAlpha},
			viewer: adminViewer(1),
		},
		{
			name:   "default_organization_lookup_failure",
			ctx:    tenancy.WithContext(t.Context(), tenancy.Context{AccountID: 1, OrganizationID: orgAlpha}),
			scope:  &platformScope{defaultErr: errors.New("default organization unavailable")},
			viewer: adminViewer(1),
		},
		{
			name:   "resolver_lacks_default_organization",
			ctx:    t.Context(),
			scope:  &fakeTenantScope{orgs: map[int]uuid.UUID{1: orgAlpha}},
			viewer: adminViewer(1),
		},
		{
			name: "viewer_organization_lookup_failure",
			ctx:  t.Context(),
			scope: &platformScope{
				fakeTenantScope: &fakeTenantScope{err: errors.New("membership unavailable")},
				defaultOrg:      orgAlpha,
			},
			viewer: adminViewer(1),
		},
		{
			name:   "non_admin_even_without_tenancy",
			ctx:    t.Context(),
			viewer: testViewer(1),
		},
	} {
		t.Run(boundary.name, func(t *testing.T) {
			for name, call := range platformRoutingCalls {
				t.Run(name, func(t *testing.T) {
					base := newTestService(newFakeStore())
					base.SetTenantScopeResolver(boundary.scope)
					service := &TenantGroupLimitService{Service: base}
					if err := call(boundary.ctx, service, boundary.viewer); !errors.Is(err, ErrForbidden) {
						t.Fatalf("global routing authorization = %v, want ErrForbidden", err)
					}
				})
			}
		})
	}
}

func TestBloemPlatformRoutingNilResolverKeepsSiloAuthority(t *testing.T) {
	// With no resolver the wrapper keeps Silo's account-admin authorization.
	// This context would be denied if tenancy were installed.
	ctx := tenancy.WithContext(t.Context(), tenancy.Context{AccountID: 1, OrganizationID: orgBeta})
	repo, pool := routingModeRepository(t)
	if _, err := pool.Exec(ctx, `INSERT INTO request_integrations(id,name,enabled,capability_id,plugin_config)
		VALUES('radarr','Radarr',true,'arr','{"service_kind":"radarr"}')`); err != nil {
		t.Fatal(err)
	}
	service, err := NewTenantGroupLimitService(NewService(repo, &fakeTMDBClient{detail: &tmdb.MediaDetail{ID: 550, Year: 1995}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	viewer := adminViewer(1)
	routes, err := service.ListRoutesAdmin(ctx, viewer)
	if err != nil || len(routes) != 2 {
		t.Fatalf("unsaved fallbacks: %+v error=%v", routes, err)
	}
	fallback, err := service.GetRoute(ctx, viewer, "fallback-movie")
	if err != nil || !fallback.IsFallback || fallback.Revision != 0 {
		t.Fatalf("unsaved fallback read: %+v error=%v", fallback, err)
	}
	fallback, err = service.UpdateRouteConditional(ctx, viewer, Route{ID: "fallback-movie", HD: RouteDestination{IntegrationID: "radarr"}}, 0)
	if err != nil || fallback.HD.IntegrationID != "radarr" || fallback.Revision == 0 {
		t.Fatalf("fallback write: %+v error=%v", fallback, err)
	}
	rule, err := service.CreateRoute(ctx, viewer, Route{
		MediaType: MediaTypeMovie, Name: "80s", Enabled: true,
		Conditions: RouteConditions{YearFrom: 1980, YearTo: 1989}, SkipUHD: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rule.Name = "Eighties"
	rule, err = service.UpdateRouteConditional(ctx, viewer, *rule, rule.Revision)
	if err != nil || rule.Name != "Eighties" {
		t.Fatalf("rule write: %+v error=%v", rule, err)
	}
	routes, err = service.ReorderRoutes(ctx, viewer, MediaTypeMovie, []string{rule.ID})
	if err != nil || len(routes) != 2 || routes[0].ID != rule.ID || routes[1].ID != "fallback-movie" {
		t.Fatalf("route order: %+v error=%v", routes, err)
	}
	overview, err := service.GetRoutingOverview(ctx, viewer)
	if err != nil || overview.Mode != RoutingStandard || len(overview.Standard) != 1 || overview.Standard[0].HDIntegrationID != "radarr" {
		t.Fatalf("routing read: %+v error=%v", overview, err)
	}
	overview, err = service.UpdateRoutingModeConditional(ctx, viewer, RoutingAdvanced, overview.Revision)
	if err != nil || overview.Mode != RoutingAdvanced {
		t.Fatalf("routing write: %+v error=%v", overview, err)
	}
	preview, err := service.PreviewRoute(ctx, viewer, MediaTypeMovie, 550, 0)
	if err != nil || len(preview.Tiers) != 2 || preview.Tiers[0].IntegrationID != "radarr" {
		t.Fatalf("route preview: %+v error=%v", preview, err)
	}
	current, err := service.GetRoute(ctx, viewer, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteRouteConditional(ctx, viewer, rule.ID, current.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetRoute(ctx, rule.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted rule still stored: %v", err)
	}

	base := newPlatformService(newFakeStore(), orgBeta, orgAlpha)
	if _, err := base.ListRoutesAdmin(ctx, adminViewer(1)); err != nil {
		t.Fatalf("undecorated upstream route administration changed: %v", err)
	}
}

func TestBloemPlatformRoutingUsesDefaultOrganizationContext(t *testing.T) {
	// The active default-organization context grants authority even when the
	// account's primary membership happens to belong to a hosted organization.
	base := newPlatformService(newFakeStore(), orgBeta, orgAlpha)
	service := &TenantGroupLimitService{Service: base}
	ctx := tenancy.WithContext(t.Context(), tenancy.Context{AccountID: 1, OrganizationID: orgAlpha})
	if routes, err := service.ListRoutesAdmin(ctx, adminViewer(1)); err != nil || len(routes) != 2 {
		t.Fatalf("default-organization routing authority: %+v error=%v", routes, err)
	}
}
