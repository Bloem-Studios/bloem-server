package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise the mounted native route with real bearer/session, tenant, profile,
// PIN-proof and acting-admin middleware. The deliberately invalid connection
// budget reaches local validation only: no provider network access is needed.
func TestBloemXtreamMountedAuthority(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if (cfg.ConnConfig.Host != "localhost" && cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "::1") ||
		(!strings.Contains(cfg.ConnConfig.Database, "test") && !strings.Contains(cfg.ConnConfig.Database, "_ci") && !strings.HasPrefix(cfg.ConnConfig.Database, "bloem_close_")) {
		t.Fatal("requires an explicitly disposable loopback test database")
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	exec := func(t *testing.T, query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"users", "organizations", "organization_memberships", "user_profiles", "access_groups", "auth_sessions"} {
		exec(t, "CREATE TEMP TABLE "+pgx.Identifier{table}.Sanitize()+" (LIKE public."+pgx.Identifier{table}.Sanitize()+" INCLUDING DEFAULTS INCLUDING INDEXES) ON COMMIT PRESERVE ROWS")
	}
	org := uuid.New()
	exec(t, `INSERT INTO users (id,email,username,password_hash,role) VALUES
		(7,'xtream-admin@example.test','xtream-admin','unused','admin'),
		(8,'xtream-user@example.test','xtream-user','unused','user')`)
	exec(t, `INSERT INTO organizations (id,slug,name,status,owner_account_id,is_default) VALUES ($1,'xtream-fixture','Xtream fixture','active',7,true)`, org)
	exec(t, `INSERT INTO access_groups (id,organization_id,name,allowed_permissions,configuration_revision) VALUES (1,$1,'Fixture','{}',1)`, org)
	exec(t, `INSERT INTO organization_memberships (id,organization_id,account_id,status,legacy_role,access_group_id,permissions) VALUES
		($1,$3,7,'active','admin',1,'{}'),($2,$3,8,'active','user',1,'{}')`, uuid.New(), uuid.New(), org)
	exec(t, `INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id,is_primary,is_child) VALUES
		('primary',7,'Primary',$1,1,true,false),('secondary',7,'Secondary',$1,1,false,false),
		('child',7,'Child',$1,1,false,true),('foreign',8,'Foreign',$1,1,true,false)`, org)
	exec(t, `INSERT INTO auth_sessions (id,user_id,expires_at) VALUES ('xtream-admin-login',7,now()+interval '1 hour'),('xtream-user-login',8,now()+interval '1 hour')`)

	const signingKey = "synthetic-xtream-route-signing-key"
	appConfig := &config.Config{}
	appConfig.Auth.JWTSecret = signingKey
	tokens := auth.NewJWTService(signingKey, time.Minute, time.Hour)
	authMW := apimw.NewAuthMiddleware(tokens, auth.NewSessionRepository(pool), nil, nil)
	tenantMW := apimw.NewTenantMiddleware(tenancy.NewResolver(tenancy.NewStore(pool)))
	stores := pgstore.NewPostgresProvider(pool)
	deps := Dependencies{DB: pool, Config: appConfig, UserStoreProvider: stores}
	mount := func() chi.Router {
		surface := newBloemClientSurface(deps, authMW, tenantMW, nil, apimw.RequireActingAdmin(bloemLiveTVPrimaryProfileChecker(stores)))
		router := chi.NewRouter()
		useBaseMiddleware(router, Dependencies{})
		mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, nil, bloemRouteSurfaces{Client: surface})
		authMW.SetDirectProfileRouteGuard(newDirectProfileRouteGuard(router.Match))
		return router
	}
	router := mount()
	admin, err := tokens.GenerateAccessToken(7, "admin", "xtream-admin-login")
	if err != nil {
		t.Fatal(err)
	}
	user, err := tokens.GenerateAccessToken(8, "user", "xtream-user-login")
	if err != nil {
		t.Fatal(err)
	}
	const path = NativeAPIPrefix + "/livetv/tuners/xtream"
	request := func(t *testing.T, target, bearer, profile, proof string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"url":"https://provider.invalid","username":"fixture-user-private","password":"fixture-password-private","max_connections":65}`))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		req.Header.Set("X-Profile-Id", profile)
		req.Header.Set("X-Profile-Token", proof)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("status=%d want=%d", rec.Code, want)
		}
		if strings.Contains(rec.Body.String(), "fixture-user-private") || strings.Contains(rec.Body.String(), "fixture-password-private") {
			t.Fatal("response disclosed submitted credentials")
		}
		return rec
	}
	t.Run("unconfigured storage", func(t *testing.T) {
		rec := request(t, path, admin, "primary", "", http.StatusServiceUnavailable)
		if !strings.Contains(rec.Body.String(), "dependency_unavailable") {
			t.Fatal("unconfigured source must fail closed")
		}
	})
	cipher, err := secret.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	deps.SecretCipher = cipher
	router = mount() // The native constructor must wire the fallback service too.
	delivery, err := streamtoken.Sign(streamtoken.Claims{SessionID: "fixture-session", PlayMethod: handlers.BloemLiveHLSStreamPurpose, UserID: 7, ProfileID: "primary"}, signingKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		UserID: 7, Role: "admin", SessionID: "xtream-admin-login", TokenType: auth.TokenTypeAccess,
		AuthMethod: auth.AuthMethodDirectProfile, ProfileID: "primary",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}).SignedString([]byte(signingKey))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, target, bearer, profile string
		want                          int
	}{
		{"unauthenticated", path, "", "primary", 401},
		{"malformed bearer", path, "invalid", "primary", 401},
		{"delivery proof cannot administer", path + "?st=" + delivery, "", "primary", 401},
		{"direct profile cannot administer", path, direct, "primary", 403},
		{"nonadmin", path, user, "foreign", 403},
		{"profile required", path, admin, "", 400},
		{"foreign profile", path, admin, "foreign", 404},
		{"unknown profile", path, admin, "unknown", 404},
		{"secondary profile", path, admin, "secondary", 403},
		{"child profile", path, admin, "child", 403},
		{"primary admin reaches validation", path, admin, "primary", 400},
		{"no v1 alias", "/api/v1/livetv/tuners/xtream", admin, "primary", 404},
		{"no v2 alias", "/api/v2/livetv/tuners/xtream", admin, "primary", 404},
	} {
		t.Run(tc.name, func(t *testing.T) { request(t, tc.target, tc.bearer, tc.profile, "", tc.want) })
	}
	t.Run("PIN proof remains session and policy bound", func(t *testing.T) {
		exec(t, `UPDATE user_profiles SET pin_hash='fixture-nonempty-hash' WHERE id='primary'`)
		defer exec(t, `UPDATE user_profiles SET pin_hash='' WHERE id='primary'`)
		request(t, path, admin, "primary", "", http.StatusForbidden)
		pin := access.NewProfileTokenService(signingKey, time.Minute)
		for _, tc := range []struct {
			session  string
			revision int64
			want     int
		}{
			{"another-login", 1, 403},
			{"xtream-admin-login", 2, 403},
			{"xtream-admin-login", 1, 400},
		} {
			proof, _, err := pin.Mint(access.ProfileTokenClaims{UserID: 7, SessionID: tc.session, ProfileID: "primary", PolicyRevision: tc.revision})
			if err != nil {
				t.Fatal(err)
			}
			request(t, path, admin, "primary", proof, tc.want)
		}
	})
	t.Run("suspended membership", func(t *testing.T) {
		exec(t, `UPDATE organization_memberships SET status='suspended' WHERE account_id=7`)
		defer exec(t, `UPDATE organization_memberships SET status='active' WHERE account_id=7`)
		request(t, path, admin, "primary", "", http.StatusForbidden)
	})
	t.Run("expired account session", func(t *testing.T) {
		exec(t, `UPDATE auth_sessions SET expires_at=now()-interval '1 minute' WHERE id='xtream-admin-login'`)
		request(t, path, admin, "primary", "", http.StatusUnauthorized)
	})
}
