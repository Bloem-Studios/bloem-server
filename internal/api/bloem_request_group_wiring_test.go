package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// A service-level adapter test does not establish that Bloem installs it. The
// router must do so even when lifecycle configuration is absent.
func TestBloemRouterScopesRequestGroupLimitsWithoutLifecycleConfig(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	var groupID int64
	if err := pool.QueryRow(t.Context(), `SELECT id FROM access_groups WHERE is_default ORDER BY id LIMIT 1`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	service := mediarequests.NewService(mediarequests.NewRepository(pool, nil), nil, nil)
	service.SetUserRepository(auth.NewUserRepository(pool))
	service.SetGroupPolicyProvider(access.NewTenantGroupStore(pool))
	handler := handlers.NewRequestsHandler(service)
	jwt := auth.NewJWTService("wiring-test-secret", time.Hour, time.Hour)
	sessions := auth.NewSessionRepository(pool)
	authMW := apimw.NewAuthMiddleware(jwt, sessions, nil, nil)
	wireBloemRouter(chi.NewRouter(), Dependencies{DB: pool}, nil, bloemLifecycleTargets{requests: handler}, authMW, nil, nil)
	limits, ok := handler.Service().(interface {
		GetGroupLimit(context.Context, mediarequests.Viewer, int64) (*mediarequests.GroupLimit, error)
	})
	if !ok {
		t.Fatal("composed request service lost group-limit support")
	}
	if _, err := limits.GetGroupLimit(t.Context(), mediarequests.Viewer{UserID: 1, IsAdmin: true}, groupID); !errors.Is(err, mediarequests.ErrForbidden) {
		t.Fatalf("production request service accepted missing tenant scope: %v", err)
	}
	// This fixture owns a disposable database: enabling requests here cannot
	// change the shared integration database's global request setting.
	repository := mediarequests.NewRepository(pool, nil)
	if _, err := repository.UpdateSettings(t.Context(), mediarequests.Settings{RequestsEnabled: true, GlobalMaxRequests: 5, GlobalWindowDays: 7}); err != nil {
		t.Fatal(err)
	}
	var accountID int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled)
		VALUES('request-policy-wiring','request-policy-wiring@example.test','x','user',true) RETURNING id`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	var tenant tenancy.Context
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations(slug,name,status,owner_account_id)
		VALUES('request-policy-wiring','Request policy wiring','active',$1) RETURNING id,status,is_default,policy_revision`, accountID).Scan(
		&tenant.OrganizationID, &tenant.OrganizationStatus, &tenant.OrganizationDefault, &tenant.PolicyRevision); err != nil {
		t.Fatal(err)
	}
	tenant.AccountID = accountID
	var allowedGroup, blockedGroup int64
	for _, row := range []struct {
		allowed bool
		dest    *int64
	}{
		{true, &allowedGroup}, {false, &blockedGroup},
	} {
		if err := pool.QueryRow(t.Context(), `INSERT INTO access_groups(organization_id,name,requests_allowed)
			VALUES($1,$2,$3) RETURNING id`, tenant.OrganizationID, uuid.NewString(), row.allowed).Scan(row.dest); err != nil {
			t.Fatal(err)
		}
	}
	maxRequests, windowDays := 2, 9
	if _, err := repository.UpsertGroupLimitConditional(t.Context(), mediarequests.GroupLimit{
		GroupID: allowedGroup, LimitMode: mediarequests.LimitModeCustom, MaxRequests: &maxRequests, WindowDays: &windowDays, ApprovalMode: mediarequests.ApprovalModeAuto,
	}, 0); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role)
		SELECT $1,$2,'active','user'
		RETURNING id,status,security_revision`, tenant.OrganizationID, accountID).Scan(
		&tenant.MembershipID, &tenant.MembershipStatus, &tenant.SecurityRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET access_group_id=$2 WHERE id=$1`, accountID, blockedGroup); err != nil {
		t.Fatal(err)
	}
	profiles := []struct {
		id      string
		group   int64
		allowed bool
	}{{"request-wiring-allowed", allowedGroup, true}, {"request-wiring-blocked", blockedGroup, false}}
	for _, row := range profiles {
		if _, err := pool.Exec(t.Context(), `INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id)
			VALUES($1,$2,$1,$3,$4)`, row.id, accountID, tenant.OrganizationID, row.group); err != nil {
			t.Fatal(err)
		}
	}
	policyService, ok := handler.Service().(interface {
		EffectivePolicy(context.Context, int) (mediarequests.EffectivePolicy, error)
	})
	if !ok {
		t.Fatal("composed service lost request policy support")
	}
	ctx := tenancy.WithContext(t.Context(), tenant)
	ctx = access.SetScope(ctx, access.Scope{UserID: accountID, ProfileID: profiles[0].id, ProfileVerified: true})
	resolved, err := policyService.EffectivePolicy(ctx, accountID)
	if err != nil || resolved.Blocked || resolved.MaxRequests != 2 || resolved.WindowDays != 9 || !resolved.AutoApprove {
		t.Fatalf("production composition lost profile policy: %+v, %v", resolved, err)
	}
	if _, err := policyService.EffectivePolicy(t.Context(), accountID); !errors.Is(err, mediarequests.ErrForbidden) {
		t.Fatalf("production request policy accepted missing scope: %v", err)
	}
	if err := handler.EnableTenantRequestPolicy(); err != nil {
		t.Fatalf("repeated installation: %v", err)
	}
	requestLifecycle, ok := handler.Service().(apiv2.RequestLifecycleService)
	if !ok {
		t.Fatal("composed service lost the v2 lifecycle capability")
	}
	// Use the actual handler service and viewer resolver on the v2 listener.
	// Only attaching already-resolved tenancy is supplied by the fixture.
	v2deps := apiv2.Dependencies{
		Auth: authMW, RequestLifecycle: requestLifecycle,
		ViewerAccess: apimw.NewViewerAccessMiddleware(access.NewResolver(auth.NewUserRepository(pool), pgstore.NewPostgresProvider(pool), nil, access.NewTenantGroupStore(pool))),
	}
	v2deps.TenantIdentity = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(tenancy.WithContext(r.Context(), tenant)))
		})
	}
	v2 := apiv2.NewHandler(v2deps)
	sessionID := uuid.NewString()
	if err := sessions.Create(t.Context(), models.AuthSession{ID: sessionID, UserID: accountID, DeviceName: "Request policy wiring", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	token, err := jwt.GenerateAccessToken(accountID, models.RoleUser, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		t.Run(profile.id, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, apiv2.Prefix+"/requests/status", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("X-Profile-Id", profile.id)
			response := httptest.NewRecorder()
			v2.ServeHTTP(response, request)
			var status apiv2.FeatureStatus
			if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || status.Allowed == nil || *status.Allowed != profile.allowed {
				t.Fatalf("v2 active-profile capability: status=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

// The admin usage operation addresses an account, not one of that account's
// profiles. Routing it to the viewer-bound policy resolver denies valid reads;
// choosing a target profile instead would report a different policy.
func TestBloemRouterAdminRequestUsageAccountTarget(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	repository := mediarequests.NewRepository(pool, nil)
	if _, err := repository.UpdateSettings(t.Context(), mediarequests.Settings{RequestsEnabled: true, GlobalMaxRequests: 5, GlobalWindowDays: 7}); err != nil {
		t.Fatal(err)
	}
	accounts := make([]int, 5)
	for i := range accounts {
		if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled)
			VALUES($1,$2,'x','admin',true) RETURNING id`, fmt.Sprintf("usage-%d", i), fmt.Sprintf("usage-%d@example.test", i)).Scan(&accounts[i]); err != nil {
			t.Fatal(err)
		}
	}
	var actor tenancy.Context
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations(slug,name,status,owner_account_id)
		VALUES('usage-tenant','Usage tenant','active',$1) RETURNING id,status,is_default,policy_revision`, accounts[0]).Scan(
		&actor.OrganizationID, &actor.OrganizationStatus, &actor.OrganizationDefault, &actor.PolicyRevision); err != nil {
		t.Fatal(err)
	}
	actor.AccountID = accounts[0]
	var foreignOrganization uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations(slug,name,status,owner_account_id)
		VALUES('usage-foreign','Foreign usage tenant','active',$1) RETURNING id`, accounts[4]).Scan(&foreignOrganization); err != nil {
		t.Fatal(err)
	}
	var accountGroup, profileGroup int64
	for _, row := range []struct {
		allowed bool
		dest    *int64
	}{{true, &accountGroup}, {false, &profileGroup}} {
		if err := pool.QueryRow(t.Context(), `INSERT INTO access_groups(organization_id,name,requests_allowed)
			VALUES($1,$2,$3) RETURNING id`, actor.OrganizationID, uuid.NewString(), row.allowed).Scan(row.dest); err != nil {
			t.Fatal(err)
		}
	}
	maximum, days := 4, 11
	if _, err := repository.UpsertGroupLimitConditional(t.Context(), mediarequests.GroupLimit{
		GroupID: accountGroup, LimitMode: mediarequests.LimitModeCustom, MaxRequests: &maximum, WindowDays: &days, ApprovalMode: mediarequests.ApprovalModeAuto,
	}, 0); err != nil {
		t.Fatal(err)
	}
	for i, account := range accounts {
		org, role, status := actor.OrganizationID, models.RoleUser, tenancy.MembershipActive
		group := &accountGroup
		if i == 0 {
			role = models.RoleAdmin
		}
		if i == 2 {
			status = tenancy.MembershipSuspended
		}
		if i == 4 {
			org = foreignOrganization
			group = nil
		}
		var membership uuid.UUID
		var revision int64
		if err := pool.QueryRow(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role)
			SELECT $1,$2,$3,$4
			RETURNING id,security_revision`, org, account, status, role).Scan(&membership, &revision); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `UPDATE users SET access_group_id=$2 WHERE id=$1`, account, group); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			actor.MembershipID, actor.SecurityRevision, actor.MembershipStatus = membership, revision, status
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET enabled=false WHERE id=$1`, accounts[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id)
		VALUES('usage-target-profile',$1,'Target profile',$2,$3)`, accounts[1], actor.OrganizationID, profileGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id,is_primary)
		VALUES('usage-admin-profile',$1,'Admin profile',$2,$3,true)`, accounts[0], actor.OrganizationID, accountGroup); err != nil {
		t.Fatal(err)
	}
	// Usage keeps the shared account-wide consumption rule. This request was
	// filed in the target's default organization and still consumes its quota.
	if _, err := repository.CreateRequest(t.Context(), mediarequests.CreateRequestRecord{
		ID: uuid.NewString(), Input: mediarequests.CreateRequestInput{MediaType: mediarequests.MediaTypeMovie, TMDBID: 123, Title: "Usage target request"},
		Requester: mediarequests.Viewer{UserID: accounts[1]},
	}); err != nil {
		t.Fatal(err)
	}
	service := mediarequests.NewService(repository, nil, nil)
	service.SetUserRepository(auth.NewUserRepository(pool))
	service.SetGroupPolicyProvider(access.NewTenantGroupStore(pool))
	requestHandler := handlers.NewRequestsHandler(service)
	jwt := auth.NewJWTService("usage-wiring-secret", time.Hour, time.Hour)
	sessions := auth.NewSessionRepository(pool)
	authMW := apimw.NewAuthMiddleware(jwt, sessions, nil, nil)
	wireBloemRouter(chi.NewRouter(), Dependencies{DB: pool}, nil, bloemLifecycleTargets{requests: requestHandler}, authMW, nil, nil)
	usage := requestHandler.Service().(apiv2.AdminRequestUsageService)
	v2deps := apiv2.Dependencies{Auth: authMW, AdminRequestUsage: usage, ActingAdmin: apimw.NewActingAdminDecider(nil, nil)}
	v2deps.AdminAccounts = handlers.NewAdminHandler(auth.NewUserRepository(pool), pool, pgstore.NewPostgresProvider(pool))
	v2deps.ViewerAccess = apimw.NewViewerAccessMiddleware(access.NewResolver(auth.NewUserRepository(pool), pgstore.NewPostgresProvider(pool), nil, access.NewTenantGroupStore(pool)))
	wireBloemV2(&v2deps, Dependencies{DB: pool}, nil, authMW)
	v2deps.TenantIdentity = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(tenancy.WithContext(r.Context(), actor)))
		})
	}
	v2 := apiv2.NewHandler(v2deps)
	sessionID := uuid.NewString()
	if err := sessions.Create(t.Context(), models.AuthSession{ID: sessionID, UserID: actor.AccountID, DeviceName: "Usage wiring", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	token, err := jwt.GenerateAccessToken(actor.AccountID, models.RoleAdmin, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	read := func(account int) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("%s/admin/request-users/%d/usage", apiv2.Prefix, account), nil)
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-Profile-Id", "usage-admin-profile")
		response := httptest.NewRecorder()
		v2.ServeHTTP(response, request)
		return response
	}
	t.Run("same organization uses target membership without inferring profile", func(t *testing.T) {
		response := read(accounts[1])
		var result apiv2.AdminRequestUserUsage
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || !result.Allowed || result.MaxRequests != 4 || result.WindowDays != 11 || !result.AutoApprove || result.Used != 1 || result.Remaining != 3 {
			t.Fatalf("account-target usage: status=%d body=%s", response.Code, response.Body)
		}
	})
	t.Run("database-less composition retains ordinary account policy", func(t *testing.T) {
		ordinary := mediarequests.NewService(repository, nil, nil)
		ordinary.SetUserRepository(auth.NewUserRepository(pool))
		ordinary.SetGroupPolicyProvider(access.NewGroupStore(pool))
		deps := apiv2.Dependencies{AdminRequestUsage: ordinary}
		wireBloemV2(&deps, Dependencies{}, nil, authMW)
		result, err := deps.AdminRequestUsage.EffectivePolicy(t.Context(), accounts[1])
		if err != nil || result.Blocked || result.MaxRequests != 5 || result.WindowDays != 7 || result.Used != 1 || result.Remaining != 4 {
			t.Fatalf("ordinary account usage after DB-less composition: %+v, %v", result, err)
		}
	})
	for _, row := range []struct {
		name    string
		account int
	}{{"suspended target", accounts[2]}, {"disabled target", accounts[3]}, {"foreign target", accounts[4]}} {
		t.Run(row.name, func(t *testing.T) {
			response := read(row.account)
			if response.Code != http.StatusForbidden {
				t.Fatalf("inactive/foreign usage: status=%d body=%s", response.Code, response.Body)
			}
		})
	}
	t.Run("target membership override", func(t *testing.T) {
		if _, err := pool.Exec(t.Context(), `UPDATE users SET requests_allowed=false WHERE id = $2 AND EXISTS (SELECT 1 FROM organization_memberships m WHERE m.account_id = users.id AND m.organization_id = $1)`, actor.OrganizationID, accounts[1]); err != nil {
			t.Fatal(err)
		}
		response := read(accounts[1])
		var result apiv2.AdminRequestUserUsage
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || result.Allowed || result.MaxRequests != 4 || result.Used != 1 {
			t.Fatalf("membership override usage: status=%d body=%s", response.Code, response.Body)
		}
	})
	t.Run("account limit takes precedence", func(t *testing.T) {
		maxRequests, windowDays := 2, 3
		if _, err := repository.UpsertUserLimit(t.Context(), mediarequests.UserLimit{
			UserID: accounts[1], LimitMode: mediarequests.LimitModeCustom, MaxRequests: &maxRequests, WindowDays: &windowDays, ApprovalMode: mediarequests.ApprovalModeManual,
		}); err != nil {
			t.Fatal(err)
		}
		response := read(accounts[1])
		var result apiv2.AdminRequestUserUsage
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || result.MaxRequests != 2 || result.WindowDays != 3 || result.AutoApprove || result.Used != 1 || result.Remaining != 1 {
			t.Fatalf("account limit usage: status=%d body=%s", response.Code, response.Body)
		}
	})
	t.Run("missing tenant authority", func(t *testing.T) {
		if _, err := v2deps.AdminRequestUsage.EffectivePolicy(t.Context(), accounts[1]); !errors.Is(err, mediarequests.ErrForbidden) {
			t.Fatalf("missing admin usage tenant: %v", err)
		}
	})
	t.Run("stale actor membership revision", func(t *testing.T) {
		actor.SecurityRevision++
		response := read(accounts[1])
		actor.SecurityRevision--
		if response.Code != http.StatusForbidden {
			t.Fatalf("stale actor usage: status=%d body=%s", response.Code, response.Body)
		}
	})
	t.Run("live admin demotion rejects stale admin claims", func(t *testing.T) {
		if err := pool.QueryRow(t.Context(), `UPDATE organization_memberships SET legacy_role='user'
			WHERE id=$1
			RETURNING security_revision`, actor.MembershipID).Scan(&actor.SecurityRevision); err != nil {
			t.Fatal(err)
		}
		response := read(accounts[1])
		if response.Code != http.StatusForbidden {
			t.Fatalf("demoted actor usage: status=%d body=%s", response.Code, response.Body)
		}
	})
}
