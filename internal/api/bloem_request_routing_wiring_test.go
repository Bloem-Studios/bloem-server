package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/metadata/tmdb"
	"github.com/Silo-Server/silo-server/internal/models"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TestBloemPlatformRequestRoutingWiring exercises production request-service
// composition, authentication, tenant resolution, acting-admin and viewer gates.
func TestBloemPlatformRequestRoutingWiring(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	// An account belongs to one organization: the platform administrator is a
	// member of the default organization, and the tenant-bound platform-role
	// administrator is its own account.
	var platform, hosted, member int
	for _, row := range []struct {
		name, role string
		id         *int
	}{{"platform", "admin", &platform}, {"hosted-admin", "admin", &hosted}, {"tenant-admin", "user", &member}} {
		if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled)
			VALUES($1,$2,'x',$3,true) RETURNING id`, row.name, row.name+"@example.test", row.role).Scan(row.id); err != nil {
			t.Fatal(err)
		}
	}
	var organization, defaults uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM organizations WHERE is_default`).Scan(&defaults); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations(slug,name,status,owner_account_id)
		VALUES('routing-tenant','Routing tenant','active',$1) RETURNING id`, member).Scan(&organization); err != nil {
		t.Fatal(err)
	}
	var defaultGroup, tenantGroup int64
	if err := pool.QueryRow(t.Context(), `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, defaults).Scan(&defaultGroup); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO access_groups(organization_id,name) VALUES($1,'Routing tenant group') RETURNING id`, organization).Scan(&tenantGroup); err != nil {
		t.Fatal(err)
	}
	for _, account := range []int{hosted, member} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role)
			SELECT $1,$2,'active','admin'`, organization, account); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `INSERT INTO user_profiles(id,user_id,name,organization_id,is_primary,access_group_id)
			VALUES($1,$2,'Tenant primary',$3,true,$4)`, fmt.Sprintf("routing-profile-%d", account), account, organization, tenantGroup); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role)
		SELECT $1,$2,'active','admin'
		ON CONFLICT(organization_id,account_id) DO NOTHING`, defaults, platform); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO user_profiles(id,user_id,name,organization_id,is_primary,access_group_id)
		VALUES('routing-platform-default',$1,'Platform primary',$2,true,$3)`, platform, defaults, defaultGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO request_routes(id,media_type,position,name,conditions,skip_uhd)
		VALUES('routing-probe','movie',0,'Platform route','{"anime":true}',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO request_integrations(id,name,enabled,capability_id,plugin_config)
        VALUES('routing-radarr','Routing Radarr',true,'arr','{"service_kind":"radarr"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO request_routes(id,media_type,position,name,is_fallback,hd_integration_id)
        VALUES('fallback-movie','movie',1000,'Everything else',true,'routing-radarr')`); err != nil {
		t.Fatal(err)
	}
	repo := mediarequests.NewRepository(pool, nil)
	service := mediarequests.NewService(repo, routingAuthorityTMDB{}, nil)
	service.SetUserRepository(auth.NewUserRepository(pool))
	service.SetGroupPolicyProvider(access.NewTenantGroupStore(pool))
	service.SetTenantScopeResolver(tenancy.NewStore(pool))
	handler := handlers.NewRequestsHandler(service)
	const signingKey = "routing-authority-test-secret"
	tokens := auth.NewJWTService(signingKey, time.Hour, time.Hour)
	sessions := auth.NewSessionRepository(pool)
	authMW := apimw.NewAuthMiddleware(tokens, sessions, nil, nil)
	tenantResolver := tenancy.NewResolver(tenancy.NewStore(pool))
	tenantMW := apimw.NewTenantMiddleware(tenantResolver)
	wireBloemRouter(chi.NewRouter(), Dependencies{DB: pool}, tenantMW, handler, nil, authMW, nil, nil)
	v2deps := apiv2.Dependencies{Auth: authMW, AdminRequests: handler.Service(), ActingAdmin: apimw.NewActingAdminDecider(nil, nil), ViewerAccess: apimw.NewViewerAccessMiddleware(access.NewResolver(auth.NewUserRepository(pool), pgstore.NewPostgresProvider(pool), nil, access.NewTenantGroupStore(pool)))}
	wireBloemV2(&v2deps, Dependencies{DB: pool}, tenantMW, authMW)
	v2 := apiv2.NewHandler(v2deps)
	token := func(account int, role string, bound bool) string {
		sessionID := uuid.NewString()
		if err := sessions.Create(t.Context(), models.AuthSession{ID: sessionID, UserID: account, DeviceName: "Routing authority test", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if !bound {
			value, err := tokens.GenerateAccessToken(account, role, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
		context, err := tenantResolver.Resolve(t.Context(), account, &organization, false)
		if err != nil {
			t.Fatal(err)
		}
		value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &auth.Claims{UserID: account, Role: role, SessionID: sessionID, TokenType: auth.TokenTypeAccess,
			OrganizationID: organization.String(), MembershipID: context.MembershipID.String(), PolicyRevision: context.PolicyRevision, SecurityRevision: context.SecurityRevision,
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString([]byte(signingKey))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	// Current account login issuers omit tenant claims, and direct-profile
	// issuers use RoleUser. This signed fixture exercises a conditional service
	// boundary if an admin context is tenant-bound; it does not demonstrate an
	// organization-only admin obtaining platform authority through login.
	platformTenant := token(hosted, models.RoleAdmin, true)
	platformDefault := token(platform, models.RoleAdmin, false)
	tenantOnly := token(member, models.RoleUser, true)
	request := func(method, path, body, token, profile, match string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, apiv2.Prefix+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Profile-Id", profile)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		response := httptest.NewRecorder()
		v2.ServeHTTP(response, r)
		return response
	}
	globalRows := func() string {
		t.Helper()
		var routes, routing string
		if err := pool.QueryRow(t.Context(), `SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.id),'[]'::jsonb)::text FROM request_routes r`).Scan(&routes); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(t.Context(), `SELECT to_jsonb(r)::text FROM request_routing r WHERE id`).Scan(&routing); err != nil {
			t.Fatal(err)
		}
		return routes + routing
	}
	before := globalRows()

	ruleBody := `{"media_type":"movie","name":"Unauthorized route","enabled":true,"conditions":{"year_from":1980,"year_to":1989},"hd":{},"uhd":{},"skip_uhd":true}`
	for _, actor := range []struct {
		name, token, profile string
	}{
		{"organization_only_admin", tenantOnly, fmt.Sprintf("routing-profile-%d", member)},
		{"conditional_hosted_bound_platform_admin", platformTenant, fmt.Sprintf("routing-profile-%d", hosted)},
	} {
		t.Run(actor.name, func(t *testing.T) {
			for _, endpoint := range []struct {
				method, path, body, match string
			}{
				{http.MethodGet, "/admin/request-routes", "", ""},
				{http.MethodGet, "/admin/request-routes/routing-probe", "", ""},
				{http.MethodGet, "/admin/request-routing", "", ""},
				{http.MethodPost, "/admin/request-routes/preview", `{"media_type":"movie","tmdb_id":550}`, ""},
				{http.MethodPost, "/admin/request-routes", ruleBody, ""},
				{http.MethodPut, "/admin/request-routes/routing-probe", ruleBody, "*"},
				{http.MethodPost, "/admin/request-routes/order", `{"media_type":"movie","ids":["routing-probe"]}`, ""},
				{http.MethodPut, "/admin/request-routing", `{"mode":"advanced"}`, "*"},
				{http.MethodDelete, "/admin/request-routes/routing-probe", "", "*"},
			} {
				response := request(endpoint.method, endpoint.path, endpoint.body, actor.token, actor.profile, endpoint.match)
				if response.Code != http.StatusForbidden {
					t.Fatalf("%s %s: status=%d body=%s, want 403", endpoint.method, endpoint.path, response.Code, response.Body)
				}
			}
		})
	}
	if globalRows() != before {
		t.Fatal("denied requests changed global route or routing rows")
	}
	var routeName string
	var routeCount int
	if err := pool.QueryRow(t.Context(), `SELECT name FROM request_routes WHERE id='routing-probe'`).Scan(&routeName); err != nil || routeName != "Platform route" {
		t.Fatalf("denied requests changed the global route: name=%q error=%v", routeName, err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM request_routes`).Scan(&routeCount); err != nil || routeCount != 2 {
		t.Fatalf("denied requests changed the route set: count=%d error=%v", routeCount, err)
	}
	stored, err := repo.GetRoutingSettings(t.Context())
	if err != nil || stored.Mode != mediarequests.RoutingStandard {
		t.Fatalf("denied requests changed global routing: %+v error=%v", stored, err)
	}

	// Search reads TMDB, not global routing configuration, and retains its
	// existing acting-admin authorization even from the conditional context.
	search := request(http.MethodGet, "/admin/request-routes/titles?media_type=movie&q=fixture", "", platformTenant, fmt.Sprintf("routing-profile-%d", hosted), "")
	var titles apiv2.Collection[apiv2.AdminRequestRouteTitle]
	if err := json.Unmarshal(search.Body.Bytes(), &titles); err != nil || search.Code != http.StatusOK || len(titles.Items) != 1 || titles.Items[0].TMDBID != 550 {
		t.Fatalf("hosted admin TMDB search: status=%d body=%s error=%v", search.Code, search.Body, err)
	}

	t.Run("default_organization_platform_admin", func(t *testing.T) {
		call := func(method, path, body, match string, want int) *httptest.ResponseRecorder {
			t.Helper()
			response := request(method, path, body, platformDefault, "routing-platform-default", match)
			if response.Code != want {
				t.Fatalf("%s %s: status=%d body=%s, want %d", method, path, response.Code, response.Body, want)
			}
			return response
		}
		listed := call(http.MethodGet, "/admin/request-routes", "", "", http.StatusOK)
		var routes apiv2.Collection[apiv2.AdminRequestRoute]
		if err := json.Unmarshal(listed.Body.Bytes(), &routes); err != nil || len(routes.Items) != 3 {
			t.Fatalf("route list: %s error=%v", listed.Body, err)
		}
		current := call(http.MethodGet, "/admin/request-routes/routing-probe", "", "", http.StatusOK)
		renamed := strings.ReplaceAll(ruleBody, "Unauthorized route", "Platform rename")
		call(http.MethodPut, "/admin/request-routes/routing-probe", renamed, current.Header().Get("ETag"), http.StatusOK)
		if err := pool.QueryRow(t.Context(), `SELECT name FROM request_routes WHERE id='routing-probe'`).Scan(&routeName); err != nil || routeName != "Platform rename" {
			t.Fatalf("authorized route write: name=%q error=%v", routeName, err)
		}
		mode := call(http.MethodGet, "/admin/request-routing", "", "", http.StatusOK)
		call(http.MethodPut, "/admin/request-routing", `{"mode":"advanced"}`, mode.Header().Get("ETag"), http.StatusOK)
		stored, err := repo.GetRoutingSettings(t.Context())
		if err != nil || stored.Mode != mediarequests.RoutingAdvanced {
			t.Fatalf("authorized mode write: %+v error=%v", stored, err)
		}
		created := call(http.MethodPost, "/admin/request-routes", ruleBody, "", http.StatusCreated)
		var added apiv2.AdminRequestRoute
		if err := json.Unmarshal(created.Body.Bytes(), &added); err != nil || added.ID == "" {
			t.Fatalf("created route: %s error=%v", created.Body, err)
		}
		ordered := call(http.MethodPost, "/admin/request-routes/order", fmt.Sprintf(`{"media_type":"movie","ids":[%q,"routing-probe"]}`, added.ID), "", http.StatusOK)
		if err := json.Unmarshal(ordered.Body.Bytes(), &routes); err != nil || len(routes.Items) != 3 || routes.Items[0].ID != added.ID || routes.Items[1].ID != "routing-probe" || routes.Items[2].ID != "fallback-movie" {
			t.Fatalf("authorized route order: %s error=%v", ordered.Body, err)
		}
		preview := call(http.MethodPost, "/admin/request-routes/preview", `{"media_type":"movie","tmdb_id":550}`, "", http.StatusOK)
		var projected apiv2.AdminRequestRoutePreviewOutput
		if err := json.Unmarshal(preview.Body.Bytes(), &projected.Body); err != nil || len(projected.Body.Tiers) != 2 || projected.Body.Tiers[0].IntegrationID != "routing-radarr" {
			t.Fatalf("authorized preview: %s error=%v", preview.Body, err)
		}
		current = call(http.MethodGet, "/admin/request-routes/"+added.ID, "", "", http.StatusOK)
		call(http.MethodDelete, "/admin/request-routes/"+added.ID, "", current.Header().Get("ETag"), http.StatusNoContent)
		var remaining int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM request_routes WHERE id=$1`, added.ID).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatalf("authorized route deletion: count=%d error=%v", remaining, err)
		}
	})
}

// Only TMDB is substituted; ownership, storage, composition, authentication,
// tenant resolution and all HTTP handlers use production implementations.
type routingAuthorityTMDB struct{ mediarequests.TMDBClient }

func (routingAuthorityTMDB) SearchMedia(context.Context, string, string, int) (*tmdb.MediaPage, error) {
	return &tmdb.MediaPage{Results: []tmdb.MediaResult{{ID: 550, MediaType: "movie", Title: "Routing fixture", Year: 1995}}}, nil
}

func (routingAuthorityTMDB) GetMediaDetail(context.Context, string, int) (*tmdb.MediaDetail, error) {
	return &tmdb.MediaDetail{ID: 550, MediaType: "movie", Title: "Routing fixture", Year: 1995}, nil
}
