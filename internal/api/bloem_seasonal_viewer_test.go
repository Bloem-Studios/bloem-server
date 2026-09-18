package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type seasonalMountTokens struct{ claims *auth.Claims }

func (s seasonalMountTokens) ValidateToken(string) (*auth.Claims, error) { return s.claims, nil }

type seasonalMountSessions struct{}

func (seasonalMountSessions) IsValid(context.Context, string) (bool, error) { return true, nil }

type seasonalMountTenant struct{ tenant tenancy.Context }

func (s seasonalMountTenant) Resolve(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error) {
	return s.tenant, nil
}

type seasonalMountViewer struct{}

func (seasonalMountViewer) Resolve(context.Context, access.ResolveInput) (access.Scope, error) {
	return access.Scope{}, access.ErrProfileUnverified
}

type seasonalMountProfiles struct{}

func (seasonalMountProfiles) ProfileOrganization(context.Context, int, string) (uuid.UUID, error) {
	return uuid.Nil, tenancy.ErrTenantNotFoundOrHidden
}

func TestBloemSeasonalViewerMountRequiresAllAuthorityResolvers(t *testing.T) {
	h := handlers.NewBloemSeasonalViewerHandler(ambience.NewService(nil, nil, nil), seasonalMountProfiles{})
	for _, missing := range []string{"none", "handler", "auth", "tenant", "viewer"} {
		t.Run(missing, func(t *testing.T) {
			handler := h
			client := bloemClientSurface{
				auth:   apimw.NewAuthMiddleware(seasonalMountTokens{}, seasonalMountSessions{}, nil, nil),
				tenant: apimw.NewTenantMiddleware(seasonalMountTenant{}),
				viewer: apimw.NewViewerAccessMiddleware(seasonalMountViewer{}),
			}
			switch missing {
			case "handler":
				handler = nil
			case "auth":
				client.auth = nil
			case "tenant":
				client.tenant = nil
			case "viewer":
				client.viewer = nil
			}
			system := handlers.NewBloemSystemHandler(nil)
			router := chi.NewRouter()
			mountBloemRoutes(router, system, nil, client.auth, nil, bloemRouteSurfaces{Client: client, SeasonalViewer: handler})
			probe := httptest.NewRecorder()
			router.ServeHTTP(probe, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/capabilities", nil))
			if strings.Contains(probe.Body.String(), "seasonal_viewer_v1") != (missing == "none") {
				t.Fatalf("untruthful capability: %s", probe.Body.String())
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/ambience", nil))
			want := http.StatusNotFound
			if missing == "none" {
				want = http.StatusUnauthorized
			}
			if rec.Code != want {
				t.Fatalf("status = %d, want %d", rec.Code, want)
			}
		})
	}
}

func TestBloemSeasonalViewerMountPreservesPINAndDirectProfileBoundaries(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "PIN required", true: "direct profile denied"}[direct], func(t *testing.T) {
			claims := &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}
			if direct {
				claims.AuthMethod = auth.AuthMethodDirectProfile
				claims.ProfileID = "viewer"
			}
			authMW := apimw.NewAuthMiddleware(seasonalMountTokens{claims: claims}, seasonalMountSessions{}, nil, nil)
			client := bloemClientSurface{
				auth:   authMW,
				tenant: apimw.NewTenantMiddleware(seasonalMountTenant{tenant: tenancy.Context{AccountID: 7, OrganizationID: uuid.New()}}),
				viewer: apimw.NewViewerAccessMiddleware(seasonalMountViewer{}),
			}
			router := chi.NewRouter()
			router.Route(NativeAPIPrefix, func(r chi.Router) {
				mountBloemSeasonalViewer(r, handlers.NewBloemSeasonalViewerHandler(ambience.NewService(nil, nil, nil), seasonalMountProfiles{}), client, handlers.NewBloemSystemHandler(nil))
			})
			authMW.SetDirectProfileRouteGuard(newDirectProfileRouteGuard(router.Match))
			req := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/ambience", nil)
			req.Header.Set("Authorization", "Bearer fixture")
			req.Header.Set("X-Profile-Id", "viewer")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if !direct && !strings.Contains(rec.Body.String(), "profile_unverified") {
				t.Fatalf("PIN check not applied: %s", rec.Body.String())
			}
			if direct && directProfileRouteAllowed(http.MethodGet, NativeAPIPrefix+"/ambience") {
				t.Fatal("direct-profile allowlist expanded")
			}
		})
	}
}
