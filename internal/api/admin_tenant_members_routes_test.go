package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/jellycompat"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

var adminTenantMemberRouteContract = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/v1/admin/tenants/{tenant_id}/members"},
	{http.MethodPost, "/api/v1/admin/tenants/{tenant_id}/members"},
	{http.MethodGet, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}"},
	{http.MethodPut, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}"},
	{http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}"},
	{http.MethodPost, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/suspend"},
	{http.MethodPost, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/resume"},
	{http.MethodPost, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/reset-password"},
	{http.MethodGet, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/profiles"},
	{http.MethodPost, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/profiles"},
	{http.MethodPut, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}"},
	{http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}"},
	{http.MethodGet, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/devices"},
	{http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/devices/{device_id}"},
	{http.MethodGet, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/auth-sessions"},
	{http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/auth-sessions/{session_id}"},
	{http.MethodDelete, "/api/v1/admin/tenants/{tenant_id}/members/{user_id}/auth-sessions"},
}

func TestAdminTenantMemberRoutesUseProductionAdminBoundary(t *testing.T) {
	pool := newDisposableAPIDatabase(t, "vondel_admin_tenant_members_", true)
	if err := database.RunMigrations(context.Background(), pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret: "admin-tenant-member-route-secret", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour,
	}}
	bootstrap := v1TenancyBootstrap{store: tenancy.NewStore(pool)}
	compatStore := jellycompat.NewSessionStore(24*time.Hour, time.Now)
	var compatInvalidationErr error
	router := NewRouter(Dependencies{
		DB: pool, Config: cfg, UserStoreProvider: pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap, MembershipProvisioner: bootstrap,
		OnUserSessionsRevoked: func(ctx context.Context, userID int) error {
			if err := compatStore.DeleteByUserIDContext(ctx, userID); err != nil {
				return err
			}
			return compatInvalidationErr
		},
		OnUserProfileSessionsRevoked: func(ctx context.Context, userID int, profileIDs []string) error {
			return compatStore.DeleteByUserAndProfileIDs(ctx, userID, profileIDs)
		},
	})

	routes := walkV1Routes(t, router)
	present := make(map[string]bool, len(routes))
	for _, route := range routes {
		present[route] = true
	}
	for _, route := range adminTenantMemberRouteContract {
		if pair := route.method + " " + route.path; !present[pair] {
			t.Errorf("production router omitted %s", pair)
		}
	}

	setup := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/setup", `{
		"username":"tenant-route-admin","email":"tenant-route-admin@example.test","password":"correct horse battery staple"
	}`, "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	var adminLogin v1LoginEnvelope
	if err := json.Unmarshal(setup.Body.Bytes(), &adminLogin); err != nil || adminLogin.AccessToken == "" {
		t.Fatalf("decode setup token: token present=%v err=%v", adminLogin.AccessToken != "", err)
	}
	createdUser := performJSONRequest(t, router, http.MethodPost, "/api/v1/admin/users", `{
		"username":"tenant-route-user","email":"tenant-route-user@example.test","password":"correct horse battery staple","role":"user"
	}`, adminLogin.AccessToken, nil)
	if createdUser.Code != http.StatusCreated {
		t.Fatalf("create non-admin user = %d %s", createdUser.Code, createdUser.Body.String())
	}
	userLoginResponse := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/login", `{
		"username":"tenant-route-user","password":"correct horse battery staple"
	}`, "", nil)
	if userLoginResponse.Code != http.StatusOK {
		t.Fatalf("login non-admin user = %d %s", userLoginResponse.Code, userLoginResponse.Body.String())
	}
	var userLogin v1LoginEnvelope
	if err := json.Unmarshal(userLoginResponse.Body.Bytes(), &userLogin); err != nil || userLogin.AccessToken == "" {
		t.Fatalf("decode non-admin token: token present=%v err=%v", userLogin.AccessToken != "", err)
	}

	requestPath := func(pattern string) string {
		path := strings.ReplaceAll(pattern, "{tenant_id}", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
		path = strings.ReplaceAll(path, "{user_id}", "999999")
		path = strings.ReplaceAll(path, "{profile_id}", "missing-profile")
		path = strings.ReplaceAll(path, "{device_id}", "missing-device")
		return strings.ReplaceAll(path, "{session_id}", "missing-session")
	}
	for _, route := range adminTenantMemberRouteContract {
		path := requestPath(route.path)
		headers := map[string]string{"Idempotency-Key": "route-command"}
		body := `{}`
		if route.method == http.MethodPost && route.path == "/api/v1/admin/tenants/{tenant_id}/members" {
			body = `{"username":"route-member","email":"route-member@example.test","password":"private-route-password"}`
		}
		unauthenticated := performJSONRequest(t, router, route.method, path, body, "", headers)
		if unauthenticated.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated %s %s = %d %s, want 401", route.method, path, unauthenticated.Code, unauthenticated.Body.String())
		}
		nonAdmin := performJSONRequest(t, router, route.method, path, body, userLogin.AccessToken, headers)
		if nonAdmin.Code != http.StatusForbidden {
			t.Errorf("non-admin %s %s = %d %s, want 403", route.method, path, nonAdmin.Code, nonAdmin.Body.String())
		}
		authorized := performJSONRequest(t, router, route.method, path, body, adminLogin.AccessToken, headers)
		if authorized.Code != http.StatusNotFound || !strings.Contains(authorized.Body.String(), `"error":"not_found"`) {
			t.Errorf("authorized %s %s = %d %s, want handler not_found", route.method, path, authorized.Code, authorized.Body.String())
		}
	}

	t.Run("authorized admin reaches member service and nested Task 4 handlers", func(t *testing.T) {
		createdTenant := performJSONRequest(t, router, http.MethodPost, "/api/v1/admin/tenants", `{
			"name":"Production route tenant",
			"external_ref":{"operator_id":"route-operator","service_id":"route-service"},
			"limits":{"slots":2,"transcodes":1}
		}`, adminLogin.AccessToken, nil)
		if createdTenant.Code != http.StatusCreated {
			t.Fatalf("create tenant = %d %s", createdTenant.Code, createdTenant.Body.String())
		}
		var tenant struct {
			TenantID string `json:"tenant_id"`
		}
		if err := json.Unmarshal(createdTenant.Body.Bytes(), &tenant); err != nil || tenant.TenantID == "" {
			t.Fatalf("decode tenant: id=%q err=%v body=%s", tenant.TenantID, err, createdTenant.Body.String())
		}

		memberBody := `{
			"username":"production-route-member",
			"email":"production-route-member@example.test",
			"password":"correct horse battery staple"
		}`
		memberPath := "/api/v1/admin/tenants/" + tenant.TenantID + "/members"
		memberHeaders := map[string]string{"Idempotency-Key": "production-route-member-command"}
		createdMember := performJSONRequest(t, router, http.MethodPost, memberPath, memberBody, adminLogin.AccessToken, memberHeaders)
		if createdMember.Code != http.StatusCreated {
			t.Fatalf("create member = %d %s", createdMember.Code, createdMember.Body.String())
		}
		var member struct {
			UserID   int    `json:"user_id"`
			Username string `json:"username"`
			Status   string `json:"status"`
		}
		if err := json.Unmarshal(createdMember.Body.Bytes(), &member); err != nil || member.UserID <= 0 ||
			member.Username != "production-route-member" || member.Status != "active" {
			t.Fatalf("decode member: member=%+v err=%v body=%s", member, err, createdMember.Body.String())
		}
		if strings.Contains(createdMember.Body.String(), "correct horse") || strings.Contains(createdMember.Body.String(), "password_hash") {
			t.Fatalf("member response disclosed a password secret: %s", createdMember.Body.String())
		}

		replayed := performJSONRequest(t, router, http.MethodPost, memberPath, memberBody, adminLogin.AccessToken, memberHeaders)
		if replayed.Code != http.StatusOK || !strings.Contains(replayed.Body.String(), `"user_id":`+strconv.Itoa(member.UserID)) {
			t.Fatalf("replay member = %d %s, want 200 and the original user", replayed.Code, replayed.Body.String())
		}

		profilesPath := memberPath + "/" + strconv.Itoa(member.UserID) + "/profiles"
		profiles := performJSONRequest(t, router, http.MethodGet, profilesPath, "", adminLogin.AccessToken, nil)
		if profiles.Code != http.StatusOK {
			t.Fatalf("list nested profiles = %d %s", profiles.Code, profiles.Body.String())
		}
		createdProfile := performJSONRequest(t, router, http.MethodPost, profilesPath,
			`{"name":"Production Member Profile"}`, adminLogin.AccessToken, nil)
		if createdProfile.Code != http.StatusCreated {
			t.Fatalf("create nested profile = %d %s", createdProfile.Code, createdProfile.Body.String())
		}
		var profileA struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(createdProfile.Body.Bytes(), &profileA); err != nil || profileA.ID == "" {
			t.Fatalf("decode tenant A profile: %+v %v", profileA, err)
		}

		createdTenantB := performJSONRequest(t, router, http.MethodPost, "/api/v1/admin/tenants", `{
			"name":"Production route tenant B",
			"external_ref":{"operator_id":"route-operator","service_id":"route-service-b"},
			"limits":{"slots":2,"transcodes":1}
		}`, adminLogin.AccessToken, nil)
		if createdTenantB.Code != http.StatusCreated {
			t.Fatalf("create tenant B = %d %s", createdTenantB.Code, createdTenantB.Body.String())
		}
		var tenantB struct {
			TenantID string `json:"tenant_id"`
		}
		if err := json.Unmarshal(createdTenantB.Body.Bytes(), &tenantB); err != nil || tenantB.TenantID == "" {
			t.Fatalf("decode tenant B: %+v %v", tenantB, err)
		}
		if _, err := pool.Exec(context.Background(), `INSERT INTO organization_memberships
			(organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, tenantB.TenantID, member.UserID); err != nil {
			t.Fatalf("seed shared tenant membership: %v", err)
		}
		profilesPathB := "/api/v1/admin/tenants/" + tenantB.TenantID + "/members/" + strconv.Itoa(member.UserID) + "/profiles"
		createdProfileB := performJSONRequest(t, router, http.MethodPost, profilesPathB,
			`{"name":"Production Member Profile B"}`, adminLogin.AccessToken, nil)
		if createdProfileB.Code != http.StatusCreated {
			t.Fatalf("create tenant B profile = %d %s", createdProfileB.Code, createdProfileB.Body.String())
		}
		var profileB struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(createdProfileB.Body.Bytes(), &profileB); err != nil || profileB.ID == "" {
			t.Fatalf("decode tenant B profile: %+v %v", profileB, err)
		}

		listedA := performJSONRequest(t, router, http.MethodGet, profilesPath, "", adminLogin.AccessToken, nil)
		if listedA.Code != http.StatusOK || !strings.Contains(listedA.Body.String(), profileA.ID) || strings.Contains(listedA.Body.String(), profileB.ID) {
			t.Fatalf("tenant A profiles leaked tenant B: %d %s", listedA.Code, listedA.Body.String())
		}
		foreignProfileUpdate := performJSONRequest(t, router, http.MethodPut, profilesPath+"/"+profileB.ID,
			`{"name":"Cross tenant"}`, adminLogin.AccessToken, nil)
		if foreignProfileUpdate.Code != http.StatusNotFound {
			t.Fatalf("cross-tenant profile update = %d %s, want 404", foreignProfileUpdate.Code, foreignProfileUpdate.Body.String())
		}

		if _, err := pool.Exec(context.Background(), `INSERT INTO user_devices
			(user_id,profile_id,device_id,device_name,device_platform) VALUES
			($1,$2,'tenant-device-a','A device','test'),($1,$3,'tenant-device-b','B device','test')`, member.UserID, profileA.ID, profileB.ID); err != nil {
			t.Fatalf("seed tenant devices: %v", err)
		}
		devicesAPath := memberPath + "/" + strconv.Itoa(member.UserID) + "/devices"
		devicesA := performJSONRequest(t, router, http.MethodGet, devicesAPath, "", adminLogin.AccessToken, nil)
		if devicesA.Code != http.StatusOK || !strings.Contains(devicesA.Body.String(), "tenant-device-a") || strings.Contains(devicesA.Body.String(), "tenant-device-b") {
			t.Fatalf("tenant A devices leaked tenant B: %d %s", devicesA.Code, devicesA.Body.String())
		}
		foreignDeviceDelete := performJSONRequest(t, router, http.MethodDelete, devicesAPath+"/tenant-device-b", "", adminLogin.AccessToken, nil)
		if foreignDeviceDelete.Code != http.StatusNotFound {
			t.Fatalf("cross-tenant device delete = %d %s, want 404", foreignDeviceDelete.Code, foreignDeviceDelete.Body.String())
		}

		expires := time.Now().Add(time.Hour)
		if _, err := pool.Exec(context.Background(), `INSERT INTO auth_sessions
			(id,user_id,device_name,expires_at,profile_id,profile_credential_revision,device_id,auth_method) VALUES
			('tenant-session-a',$1,'A session',$2,$3,1,'tenant-device-a','direct_profile'),
			('tenant-session-a-single',$1,'A single session',$2,$3,1,'tenant-device-a','direct_profile'),
			('tenant-session-b',$1,'B session',$2,$4,1,'tenant-device-b','direct_profile'),
			('tenant-account-session',$1,'Account session',$2,NULL,NULL,'','account')`, member.UserID, expires, profileA.ID, profileB.ID); err != nil {
			t.Fatalf("seed tenant sessions: %v", err)
		}
		for _, session := range []jellycompat.Session{
			{Token: "compat-tenant-a", StreamAppUserID: member.UserID, ProfileID: profileA.ID},
			{Token: "compat-tenant-a-single", StreamAppUserID: member.UserID, ProfileID: profileA.ID},
			{Token: "compat-tenant-b", StreamAppUserID: member.UserID, ProfileID: profileB.ID},
			{Token: "compat-account", StreamAppUserID: member.UserID},
		} {
			if err := compatStore.Put(session); err != nil {
				t.Fatalf("seed compat session %q: %v", session.Token, err)
			}
		}
		sessionsAPath := memberPath + "/" + strconv.Itoa(member.UserID) + "/auth-sessions"
		sessionsA := performJSONRequest(t, router, http.MethodGet, sessionsAPath, "", adminLogin.AccessToken, nil)
		if sessionsA.Code != http.StatusOK || !strings.Contains(sessionsA.Body.String(), "tenant-session-a") ||
			strings.Contains(sessionsA.Body.String(), "tenant-session-b") || strings.Contains(sessionsA.Body.String(), "tenant-account-session") {
			t.Fatalf("tenant A sessions leaked unattributed sessions: %d %s", sessionsA.Code, sessionsA.Body.String())
		}
		for _, foreignSession := range []string{"tenant-session-b", "tenant-account-session"} {
			response := performJSONRequest(t, router, http.MethodDelete, sessionsAPath+"/"+foreignSession, "", adminLogin.AccessToken, nil)
			if response.Code != http.StatusNotFound {
				t.Fatalf("cross-tenant session %s delete = %d %s, want 404", foreignSession, response.Code, response.Body.String())
			}
		}
		revokeSingleA := performJSONRequest(t, router, http.MethodDelete, sessionsAPath+"/tenant-session-a-single", "", adminLogin.AccessToken, nil)
		if revokeSingleA.Code != http.StatusNoContent {
			t.Fatalf("tenant revoke single = %d %s", revokeSingleA.Code, revokeSingleA.Body.String())
		}
		if _, ok := compatStore.Get("compat-tenant-a-single"); ok {
			t.Fatal("tenant A compat session remained after scoped single revoke")
		}
		if _, ok := compatStore.Get("compat-tenant-b"); !ok {
			t.Fatal("tenant A scoped single revoke deleted tenant B compat session")
		}
		if _, ok := compatStore.Get("compat-account"); !ok {
			t.Fatal("tenant A scoped single revoke deleted account-mode compat session")
		}
		revokeAllA := performJSONRequest(t, router, http.MethodDelete, sessionsAPath, "", adminLogin.AccessToken, nil)
		if revokeAllA.Code != http.StatusNoContent {
			t.Fatalf("tenant revoke all = %d %s", revokeAllA.Code, revokeAllA.Body.String())
		}
		var revokedA, revokedB, revokedAccount bool
		if err := pool.QueryRow(context.Background(), `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id='tenant-session-a'`).Scan(&revokedA); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(context.Background(), `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id='tenant-session-b'`).Scan(&revokedB); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(context.Background(), `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id='tenant-account-session'`).Scan(&revokedAccount); err != nil {
			t.Fatal(err)
		}
		if !revokedA || revokedB || revokedAccount {
			t.Fatalf("tenant revoke-all scope: a=%v b=%v account=%v", revokedA, revokedB, revokedAccount)
		}
		if _, ok := compatStore.Get("compat-tenant-a"); ok {
			t.Fatal("tenant A compat session remained after scoped revoke-all")
		}
		if _, ok := compatStore.Get("compat-tenant-b"); !ok {
			t.Fatal("tenant A scoped revoke-all deleted tenant B compat session")
		}
		if _, ok := compatStore.Get("compat-account"); !ok {
			t.Fatal("tenant A scoped revoke-all deleted account-mode compat session")
		}

		memberAPath := memberPath + "/" + strconv.Itoa(member.UserID)
		updatedMember := performJSONRequest(t, router, http.MethodPut, memberAPath,
			`{"username":"production-route-member-renamed","email":"production-route-member-renamed@example.test"}`,
			adminLogin.AccessToken, nil)
		if updatedMember.Code != http.StatusOK {
			t.Fatalf("update member identity = %d %s", updatedMember.Code, updatedMember.Body.String())
		}
		for _, token := range []string{"compat-tenant-b", "compat-account"} {
			if _, ok := compatStore.Get(token); ok {
				t.Fatalf("identity update left compat session %q valid", token)
			}
		}
		if err := compatStore.Put(jellycompat.Session{Token: "compat-reset", StreamAppUserID: member.UserID, ProfileID: profileB.ID}); err != nil {
			t.Fatal(err)
		}
		resetMember := performJSONRequest(t, router, http.MethodPost, memberAPath+"/reset-password",
			`{"password":"new production route password"}`, adminLogin.AccessToken, nil)
		if resetMember.Code != http.StatusOK || strings.Contains(resetMember.Body.String(), "new production") {
			t.Fatalf("reset member password = %d %s", resetMember.Code, resetMember.Body.String())
		}
		if _, ok := compatStore.Get("compat-reset"); ok {
			t.Fatal("password reset left compat session valid")
		}
		if err := compatStore.Put(jellycompat.Session{Token: "compat-suspend", StreamAppUserID: member.UserID, ProfileID: profileB.ID}); err != nil {
			t.Fatal(err)
		}
		suspendedMember := performJSONRequest(t, router, http.MethodPost, memberAPath+"/suspend", `{}`, adminLogin.AccessToken, nil)
		if suspendedMember.Code != http.StatusOK {
			t.Fatalf("suspend member = %d %s", suspendedMember.Code, suspendedMember.Body.String())
		}
		if _, ok := compatStore.Get("compat-suspend"); ok {
			t.Fatal("suspend left compat session valid")
		}
		resumedMember := performJSONRequest(t, router, http.MethodPost, memberAPath+"/resume", `{}`, adminLogin.AccessToken, nil)
		if resumedMember.Code != http.StatusOK {
			t.Fatalf("resume member = %d %s", resumedMember.Code, resumedMember.Body.String())
		}
		compatInvalidationErr = context.DeadlineExceeded
		committedEmail := "compat-failure-committed@example.test"
		failedCompatUpdate := performJSONRequest(t, router, http.MethodPut, memberAPath,
			`{"email":"`+committedEmail+`"}`, adminLogin.AccessToken, nil)
		if failedCompatUpdate.Code != http.StatusInternalServerError || !strings.Contains(failedCompatUpdate.Body.String(), `"error":"internal_error"`) {
			t.Fatalf("compat-failing identity update = %d %s, want surfaced 500", failedCompatUpdate.Code, failedCompatUpdate.Body.String())
		}
		compatInvalidationErr = nil
		committedMember := performJSONRequest(t, router, http.MethodGet, memberAPath, "", adminLogin.AccessToken, nil)
		if committedMember.Code != http.StatusOK || !strings.Contains(committedMember.Body.String(), `"email":"`+committedEmail+`"`) {
			t.Fatalf("identity did not remain committed after compat failure = %d %s", committedMember.Code, committedMember.Body.String())
		}
		deletedA := performJSONRequest(t, router, http.MethodDelete, memberAPath, "", adminLogin.AccessToken, nil)
		if deletedA.Code != http.StatusNoContent {
			t.Fatalf("delete tenant A membership = %d %s", deletedA.Code, deletedA.Body.String())
		}
		repeatedDeleteA := performJSONRequest(t, router, http.MethodDelete, memberAPath, "", adminLogin.AccessToken, nil)
		if repeatedDeleteA.Code != http.StatusNoContent {
			t.Fatalf("repeat delete tenant A membership = %d %s", repeatedDeleteA.Code, repeatedDeleteA.Body.String())
		}
		var userPresent, membershipBPresent, profileAPresent, profileBPresent, deviceAPresent, deviceBPresent bool
		checks := []struct {
			query string
			args  []any
			out   *bool
		}{
			{`SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, []any{member.UserID}, &userPresent},
			{`SELECT EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$1 AND account_id=$2)`, []any{tenantB.TenantID, member.UserID}, &membershipBPresent},
			{`SELECT EXISTS(SELECT 1 FROM user_profiles WHERE user_id=$1 AND id=$2)`, []any{member.UserID, profileA.ID}, &profileAPresent},
			{`SELECT EXISTS(SELECT 1 FROM user_profiles WHERE user_id=$1 AND id=$2)`, []any{member.UserID, profileB.ID}, &profileBPresent},
			{`SELECT EXISTS(SELECT 1 FROM user_devices WHERE user_id=$1 AND profile_id=$2)`, []any{member.UserID, profileA.ID}, &deviceAPresent},
			{`SELECT EXISTS(SELECT 1 FROM user_devices WHERE user_id=$1 AND profile_id=$2)`, []any{member.UserID, profileB.ID}, &deviceBPresent},
		}
		for _, check := range checks {
			if err := pool.QueryRow(context.Background(), check.query, check.args...).Scan(check.out); err != nil {
				t.Fatal(err)
			}
		}
		if !userPresent || !membershipBPresent || profileAPresent || !profileBPresent || deviceAPresent || !deviceBPresent {
			t.Fatalf("scoped delete state user=%v membership_b=%v profile_a=%v profile_b=%v device_a=%v device_b=%v",
				userPresent, membershipBPresent, profileAPresent, profileBPresent, deviceAPresent, deviceBPresent)
		}
	})
}
