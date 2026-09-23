package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This is a mounted-router/database regression, not live tuner acceptance.
// Only the encoder is a fixture: real session rows, signing, tenant/profile
// resolution and delivery handlers must carry the returned URL end to end.
func TestBloemLiveTVDeliveryPostgres(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"users", "organizations", "organization_memberships", "user_profiles", "access_groups", "auth_sessions", "livetv_tuners", "livetv_channels", "livetv_sessions", "node_heartbeats"} {
		name := pgx.Identifier{table}.Sanitize()
		exec("CREATE TEMP TABLE " + name + " (LIKE public." + name + " INCLUDING DEFAULTS INCLUDING INDEXES) ON COMMIT PRESERVE ROWS")
	}
	org, membership := uuid.New(), uuid.New()
	exec(`INSERT INTO users (id,email,username,password_hash,role) VALUES (7,'livetv@example.test','livetv','unused','user')`)
	exec(`INSERT INTO organizations (id,slug,name,status,owner_account_id,is_default) VALUES ($1,'livetv','Live TV','active',7,true)`, org)
	exec(`INSERT INTO access_groups (id,organization_id,name,allowed_permissions,configuration_revision) VALUES (1,$1,'Viewer',ARRAY['watch_live_tv'],1)`, org)
	exec(`INSERT INTO organization_memberships (id,organization_id,account_id,status,legacy_role,access_group_id,permissions) VALUES ($1,$2,7,'active','user',1,ARRAY['watch_live_tv'])`, membership, org)
	exec(`INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id) VALUES ('profile',7,'Viewer',$1,1),('sibling',7,'Sibling',$1,1)`, org)
	exec(`INSERT INTO users (id,email,username,password_hash,role) VALUES (8,'foreign@example.test','foreign','unused','user')`)
	exec(`INSERT INTO organization_memberships (id,organization_id,account_id,status,legacy_role,access_group_id,permissions) VALUES ($1,$2,8,'active','user',1,ARRAY['watch_live_tv'])`, uuid.New(), org)
	exec(`INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id) VALUES ('foreign',8,'Foreign',$1,1)`, org)
	exec(`INSERT INTO auth_sessions (id,user_id,expires_at) VALUES ('livetv-login',7,now()+interval '1 hour')`)
	exec(`INSERT INTO livetv_tuners (id,type,device_id,base_url,tuner_count,status) VALUES ('tuner','hdhomerun','fixture','http://192.0.2.1',4,'ready')`)
	exec(`INSERT INTO livetv_channels (id,tuner_id,number,name,stream_url,enabled) VALUES ('channel','tuner','1','Fixture','http://192.0.2.1/auto/v1',true)`)

	root := t.TempDir()
	encoder := filepath.Join(root, "fixture-encoder")
	if err := os.WriteFile(encoder, []byte(`#!/bin/sh
for arg in "$@"; do output="$arg"; done
dir=$(dirname "$output")
mkdir -p "$dir"
printf '#EXTM3U\n#EXT-X-TARGETDURATION:2\n' > "$output"
for i in 0 1 2 3 4; do
  printf 'fixture-segment' > "$dir/seg_0000$i.ts"
  printf '#EXTINF:2.0,\nseg_0000%s.ts\n' "$i" >> "$output"
done
while true; do sleep 1; done
`), 0700); err != nil {
		t.Fatal(err)
	}
	service := livetv.NewService(pool)
	bridge := livetv.NewHLSBridge(livetv.HLSBridgeOptions{Root: root, FFmpegPath: encoder, HWAccel: "none"})
	service.SetPlaybackBridge(bridge)
	instance := uuid.NewString()
	service.SetClusterOwner("fixture-api", instance)
	secret := "synthetic-native-livetv-delivery-secret"
	appConfig := &config.Config{}
	appConfig.Auth.JWTSecret = secret
	tokens := auth.NewJWTService(secret, time.Minute, time.Hour)
	authMW := apimw.NewAuthMiddleware(tokens, auth.NewSessionRepository(pool), nil, nil)
	tenantMW := apimw.NewTenantMiddleware(tenancy.NewResolver(tenancy.NewStore(pool)))
	deps := Dependencies{DB: pool, Config: appConfig, UserStoreProvider: pgstore.NewPostgresProvider(pool), BloemDependencies: BloemDependencies{LiveTV: service}}
	surface := newBloemClientSurface(deps, authMW, tenantMW, nil, nil)
	router := chi.NewRouter()
	useBaseMiddleware(router, Dependencies{})
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, nil, bloemRouteSurfaces{Client: surface})
	authMW.SetDirectProfileRouteGuard(newDirectProfileRouteGuard(router.Match))
	bearer, err := tokens.GenerateAccessToken(7, "user", "livetv-login")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, credential string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
			req.Header.Set("X-Profile-Id", "profile")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s delivery: status=%d want=%d body=%s", method, rec.Code, status, rec.Body.String())
		}
		return rec
	}
	start := request(http.MethodPost, "/api/bloem/v1/livetv/channels/channel/session", bearer, http.StatusCreated)
	var session livetv.SessionStartResponse
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = service.ReleaseSession(context.Background(), session.SessionID, 7, "profile", true)
	})
	u, err := url.Parse(session.HLSURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.Path, "/api/bloem/v1/livetv/live-hls/") || u.Host != "" || u.Scheme != "" {
		t.Fatalf("session URL does not target native delivery: %s", u.Path)
	}
	if session.StreamURL != session.HLSURL || u.Query().Get("st") == "" || len(u.Query()) != 1 {
		t.Fatal("session must return only a signed delivery ticket, with matching stream and HLS URLs")
	}
	playlist := request(http.MethodGet, session.HLSURL, "", http.StatusOK)
	segmentPath := ""
	for _, line := range strings.Split(playlist.Body.String(), "\n") {
		if strings.HasPrefix(line, "seg_") {
			ref, err := url.Parse(line)
			if err != nil {
				t.Fatal(err)
			}
			segmentPath = u.ResolveReference(ref).String()
			segment := request(http.MethodGet, segmentPath, "", http.StatusOK)
			if segment.Body.String() != "fixture-segment" {
				t.Fatal("segment bytes changed")
			}
			break
		}
	}
	if segmentPath == "" {
		t.Fatal("playlist did not contain a segment")
	}
	claims, err := streamtoken.Verify(u.Query().Get("st"), secret)
	if err != nil {
		t.Fatal(err)
	}
	withProof := func(proof string) string { return u.Path + "?st=" + url.QueryEscape(proof) }
	for _, tc := range []struct {
		name, purpose, profile, key, sessionID string
		user                                   int
		ttl                                    time.Duration
		want                                   int
	}{
		{"foreign signature", claims.PlayMethod, "profile", "another-server", claims.SessionID, 7, time.Hour, 401},
		{"expired", claims.PlayMethod, "profile", secret, claims.SessionID, 7, -time.Hour, 401},
		{"wrong session", claims.PlayMethod, "profile", secret, "other-session", 7, time.Hour, 401},
		{"wrong purpose", "download", "profile", secret, claims.SessionID, 7, time.Hour, 401},
		{"legacy purpose", "", "profile", secret, claims.SessionID, 7, time.Hour, 401},
		{"missing user", claims.PlayMethod, "profile", secret, claims.SessionID, 0, time.Hour, 401},
		{"missing profile", claims.PlayMethod, "", secret, claims.SessionID, 7, time.Hour, 401},
		{"foreign user", claims.PlayMethod, "foreign", secret, claims.SessionID, 8, time.Hour, 404},
		{"foreign profile owner", claims.PlayMethod, "foreign", secret, claims.SessionID, 7, time.Hour, 401},
		{"sibling profile", claims.PlayMethod, "sibling", secret, claims.SessionID, 7, time.Hour, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proof, err := streamtoken.Sign(streamtoken.Claims{SessionID: tc.sessionID, PlayMethod: tc.purpose, UserID: tc.user, ProfileID: tc.profile}, tc.key, tc.ttl)
			if err != nil {
				t.Fatal(err)
			}
			request(http.MethodGet, withProof(proof), "", tc.want)
		})
	}
	for _, proof := range []string{"", "malformed", bearer} {
		request(http.MethodGet, withProof(proof), "", http.StatusUnauthorized)
		request(http.MethodGet, withProof(proof), bearer, http.StatusUnauthorized)
	}
	for _, bad := range []streamtoken.Claims{
		{SessionID: claims.SessionID, UserID: 7, ProfileID: "profile", PlayMethod: claims.PlayMethod},
		{SessionID: claims.SessionID, UserID: 7, ProfileID: "profile", PlayMethod: claims.PlayMethod, RegisteredClaims: jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"foreign-audience"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}},
	} {
		proof, err := jwt.NewWithClaims(jwt.SigningMethodHS256, bad).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		request(http.MethodGet, withProof(proof), "", http.StatusUnauthorized)
	}
	for _, target := range []string{
		strings.Replace(session.HLSURL, "/index.m3u8", "/../index.m3u8", 1),
		strings.Replace(session.HLSURL, "/index.m3u8", "/%69ndex.m3u8", 1),
		strings.Replace(session.HLSURL, "/index.m3u8", "/encoder.log", 1),
	} {
		request(http.MethodGet, target, "", http.StatusUnauthorized)
	}
	for _, operation := range []struct{ method, path string }{
		{"GET", "/channels"}, {"GET", "/guide"}, {"GET", "/recordings"}, {"GET", "/capability"},
		{"POST", "/channels/channel/session"}, {"POST", "/recordings"}, {"POST", "/series-rules"},
		{"POST", "/sessions/" + session.SessionID + "/heartbeat"}, {"DELETE", "/sessions/" + session.SessionID},
		{"GET", "/sessions/" + session.SessionID + "/stream"},
	} {
		request(operation.method, "/api/bloem/v1/livetv"+operation.path+"?"+u.RawQuery, "", http.StatusUnauthorized)
	}
	request(http.MethodPost, session.HLSURL, "", http.StatusUnauthorized)
	request(http.MethodGet, strings.Replace(session.HLSURL, "/api/bloem/v1/", "/api/v1/", 1), "", http.StatusNotFound)
	// Request-supplied query credentials must never be copied into HLS assets.
	clean := request(http.MethodGet, session.HLSURL+"&token=account-secret&profile_id=sibling&profile_token=pin-secret", "", http.StatusOK)
	if strings.Contains(clean.Body.String(), "account-secret") || strings.Contains(clean.Body.String(), "pin-secret") || strings.Contains(clean.Body.String(), "profile_id") {
		t.Fatal("playlist forwarded non-delivery credentials")
	}
	for _, tc := range []struct{ name, change, restore string }{
		{"membership revoked", `UPDATE organization_memberships SET status='suspended' WHERE account_id=7`, `UPDATE organization_memberships SET status='active' WHERE account_id=7`},
		{"organization suspended", `UPDATE organizations SET status='suspended'`, `UPDATE organizations SET status='active'`},
		{"profile transferred", `UPDATE user_profiles SET user_id=8 WHERE id='profile'`, `UPDATE user_profiles SET user_id=7 WHERE id='profile'`},
		{"profile moved", `UPDATE user_profiles SET organization_id=gen_random_uuid() WHERE id='profile'`, `UPDATE user_profiles SET organization_id=(SELECT id FROM organizations) WHERE id='profile'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec(tc.change)
			defer exec(tc.restore)
			request(http.MethodGet, session.HLSURL, "", http.StatusUnauthorized)
		})
	}
	// Native direct-profile admission stays default-deny, including with an
	// otherwise valid delivery ticket attached to an operational request.
	directClaims := auth.Claims{UserID: 7, Role: "user", SessionID: "livetv-login", TokenType: auth.TokenTypeAccess, AuthMethod: auth.AuthMethodDirectProfile, ProfileID: "profile", RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	direct, err := jwt.NewWithClaims(jwt.SigningMethodHS256, directClaims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "/api/bloem/v1/livetv/channels/channel/session?"+u.RawQuery, direct, http.StatusForbidden)
	request(http.MethodGet, "/api/bloem/v1/livetv/channels?"+u.RawQuery, direct, http.StatusForbidden)

	// A second API node must route through the durable owner, retain the native
	// path and rerun authorization there. It has no local encoder session.
	ownerRouter := router
	owner := httptest.NewServer(ownerRouter)
	t.Cleanup(owner.Close)
	exec(`INSERT INTO node_heartbeats (node_id,node_type,node_url,instance_id,updated_at) VALUES ('fixture-api','api',$1,$2,now())`, owner.URL, instance)
	remote := livetv.NewService(pool)
	remote.SetPlaybackBridge(livetv.NewHLSBridge(livetv.HLSBridgeOptions{Root: t.TempDir(), FFmpegPath: encoder}))
	remote.SetClusterOwner("fixture-other-api", uuid.NewString())
	deps.LiveTV = remote
	router = chi.NewRouter()
	useBaseMiddleware(router, Dependencies{})
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, nil, bloemRouteSurfaces{Client: newBloemClientSurface(deps, authMW, tenantMW, nil, nil)})
	request(http.MethodGet, session.HLSURL, "", http.StatusOK)
	if got := request(http.MethodGet, segmentPath, "", http.StatusOK).Body.String(); got != "fixture-segment" {
		t.Fatal("remote segment bytes changed")
	}
	loop := httptest.NewRequest(http.MethodGet, session.HLSURL, nil)
	loop.Header.Set("X-Bloem-LiveTV-Peer-Hop", "1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, loop)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("peer loop status=%d", rec.Code)
	}
	exec(`UPDATE node_heartbeats SET instance_id=$1`, uuid.New())
	request(http.MethodGet, session.HLSURL, "", http.StatusServiceUnavailable)
	exec(`UPDATE node_heartbeats SET instance_id=$1`, instance)
	// Current grants gate each delivery and heartbeat; owned release remains
	// available so permission loss does not strand the durable tuner claim.
	exec(`UPDATE access_groups SET allowed_permissions='{}'`)
	request(http.MethodGet, session.HLSURL, "", http.StatusForbidden)
	request(http.MethodPost, "/api/bloem/v1/livetv/sessions/"+session.SessionID+"/heartbeat", bearer, http.StatusForbidden)
	request(http.MethodDelete, "/api/bloem/v1/livetv/sessions/"+session.SessionID, bearer, http.StatusOK)
	exec(`UPDATE access_groups SET allowed_permissions=ARRAY['watch_live_tv']`)
	request(http.MethodGet, session.HLSURL, "", http.StatusNotFound)

	// The unbridged MPEG-TS session returns the native authenticated proxy URL.
	// HEAD proves it reaches that handler without opening a real tuner socket.
	deps.LiveTV = livetv.NewService(pool)
	router = chi.NewRouter()
	useBaseMiddleware(router, Dependencies{})
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, nil, bloemRouteSurfaces{Client: newBloemClientSurface(deps, authMW, tenantMW, nil, nil)})
	rawStart := request(http.MethodPost, "/api/bloem/v1/livetv/channels/channel/session", bearer, http.StatusCreated)
	var raw livetv.SessionStartResponse
	if err := json.Unmarshal(rawStart.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.StreamURL != "/api/bloem/v1/livetv/sessions/"+raw.SessionID+"/stream" || raw.HLSURL != raw.StreamURL || raw.Transport != "mpegts" {
		t.Fatal("raw session did not return its native proxy URL")
	}
	request(http.MethodHead, raw.StreamURL, bearer, http.StatusOK)
	request(http.MethodHead, raw.StreamURL+"?"+u.RawQuery, "", http.StatusUnauthorized)
	request(http.MethodDelete, "/api/bloem/v1/livetv/sessions/"+raw.SessionID, bearer, http.StatusOK)
	// Exercise the real native constructor's household checker. Admin claims
	// alone must not give secondary, child or unknown profiles the override.
	exec("CREATE TEMP TABLE livetv_recordings (LIKE public.livetv_recordings INCLUDING DEFAULTS INCLUDING INDEXES) ON COMMIT PRESERVE ROWS")
	exec(`INSERT INTO livetv_recordings (id,channel_id,start_at,stop_at,user_id,profile_id,title) VALUES ('sibling-recording','channel',now(),now()+interval '1 hour',7,'sibling','Sibling recording')`)
	exec(`UPDATE users SET role='admin' WHERE id=7`)
	adminBearer, err := tokens.GenerateAccessToken(7, "admin", "livetv-login")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, profile         string
		primary, child        bool
		wantStatus, wantCount int
	}{
		{"primary admin", "profile", true, false, 200, 1},
		{"secondary admin", "profile", false, false, 200, 0},
		{"child admin", "profile", false, true, 200, 0},
		{"unknown profile", "unknown", false, false, 404, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec(`UPDATE user_profiles SET is_primary=$1,is_child=$2 WHERE id='profile'`, tc.primary, tc.child)
			req := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/livetv/recordings", nil)
			req.Header.Set("Authorization", "Bearer "+adminBearer)
			req.Header.Set("X-Profile-Id", tc.profile)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if rec.Code == http.StatusOK {
				var got livetv.RecordingsResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if len(got.Recordings) != tc.wantCount {
					t.Fatalf("recordings=%d want=%d", len(got.Recordings), tc.wantCount)
				}
			}
		})
	}

}
