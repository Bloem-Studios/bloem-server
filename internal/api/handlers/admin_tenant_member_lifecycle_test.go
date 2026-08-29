package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestTenantMemberLifecycleReplaysStoredResultWithoutRemutatingReplacement(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	users := auth.NewUserRepository(pool)
	actor, err := users.Create(ctx, models.CreateUserInput{
		Username: "tenant-lifecycle-actor-" + uuid.NewString(), Email: uuid.NewString() + "@tenant-lifecycle.test",
		Password: "test-password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	var defaultOrganization uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&defaultOrganization); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','admin')`, defaultOrganization, actor.ID); err != nil {
		t.Fatalf("create actor membership: %v", err)
	}
	store := tenancy.NewStore(pool)
	tenant, err := store.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Lifecycle tenant", ExternalOperatorID: "operator", ExternalServiceID: "service-" + uuid.NewString(), Slots: 2, Transcodes: 1,
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	accounts := auth.NewAccountProvisioner(users, pgstore.NewPostgresProvider(pool))
	memberService := tenancy.NewMemberService(pool, accounts, users, auth.NewSessionRepository(pool))
	member, _, err := memberService.Create(ctx, tenant.ID, "member-create-"+uuid.NewString(), tenancy.CreateMemberInput{
		Username: "member-" + uuid.NewString(), Email: uuid.NewString() + "@tenant-lifecycle.test", Password: "member-password",
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	t.Cleanup(func() {
		_ = store.DeleteTenantOrganization(context.Background(), tenant.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::integer[])`, []int{actor.ID, member.ID})
	})

	adminHandler := handlers.NewAdminHandler(users, pool, pgstore.NewPostgresProvider(pool))
	handler := handlers.NewAdminTenantMembersHandler(memberService, adminHandler)
	secret := []byte("tenant-member-lifecycle-handler-secret")
	handler.SetLifecycleIdempotency(
		lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(pool), lifecycleidempotency.NewHMACKeyDigester(secret)),
		lifecycleidempotency.NewRequestDigester(secret),
	)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{UserID: actor.ID, AccountIncarnationID: actor.AccountIncarnationID.String(), Role: models.RoleAdmin}
			next.ServeHTTP(w, r.WithContext(apimw.SetClaims(r.Context(), claims)))
		})
	})
	router.Put("/api/v1/admin/tenants/{tenant_id}/members/{user_id}", handler.HandleUpdate)
	router.Delete("/api/v1/admin/tenants/{tenant_id}/members/{user_id}", handler.HandleDelete)
	router.Post("/api/v1/admin/tenants/{tenant_id}/members/{user_id}/suspend", handler.HandleSuspend)
	router.Post("/api/v1/admin/tenants/{tenant_id}/members/{user_id}/resume", handler.HandleResume)
	router.Post("/api/v1/admin/tenants/{tenant_id}/members/{user_id}/reset-password", handler.HandleResetPassword)
	router.Delete("/api/v1/admin/tenants/{tenant_id}/members/{user_id}/auth-sessions/{session_id}", handler.HandleRevokeAuthSession)
	router.Delete("/api/v1/admin/tenants/{tenant_id}/members/{user_id}/auth-sessions", handler.HandleRevokeAllAuthSessions)
	path := "/api/v1/admin/tenants/" + tenant.ID.String() + "/members/" + strconv.Itoa(member.ID)
	key := "tenant-member-update-" + uuid.NewString()
	request := func(name string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"username": name})
		req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
		req.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}

	firstName := "first-result-" + uuid.NewString()
	first := request(firstName)
	if first.Code != http.StatusOK {
		t.Fatalf("first update = %d: %s", first.Code, first.Body.String())
	}
	laterName := "later-state-" + uuid.NewString()
	if _, err := memberService.Update(ctx, tenant.ID, member.ID, tenancy.UpdateMemberInput{Username: &laterName}); err != nil {
		t.Fatalf("change state after first request: %v", err)
	}
	replay := request(firstName)
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d %s; first = %d %s", replay.Code, replay.Body.String(), first.Code, first.Body.String())
	}
	got, err := memberService.Get(ctx, tenant.ID, member.ID)
	if err != nil || got.Username != laterName {
		t.Fatalf("state after replay = %+v, %v; want username %q", got, err, laterName)
	}

	suspendKey := "tenant-member-suspend-" + uuid.NewString()
	suspendRequest := httptest.NewRequest(http.MethodPost, path+"/suspend", nil)
	suspendRequest.Header.Set("Idempotency-Key", suspendKey)
	firstSuspend := httptest.NewRecorder()
	router.ServeHTTP(firstSuspend, suspendRequest)
	if firstSuspend.Code != http.StatusServiceUnavailable || firstSuspend.Header().Get("Retry-After") != "1" {
		t.Fatalf("compatibility suspend = %d %s: %s", firstSuspend.Code, firstSuspend.Header().Get("Retry-After"), firstSuspend.Body.String())
	}
	got, err = memberService.Get(ctx, tenant.ID, member.ID)
	if err != nil || !got.Enabled {
		t.Fatalf("member after refused suspend = %+v, %v; want enabled", got, err)
	}
	keyDigest := lifecycleidempotency.NewHMACKeyDigester(secret)(suspendKey)
	var suspendReceipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_request_receipts WHERE idempotency_key_digest=$1`, keyDigest[:]).Scan(&suspendReceipts); err != nil || suspendReceipts != 0 {
		t.Fatalf("refused suspend receipts = %d, %v; want none", suspendReceipts, err)
	}

	var profileID string
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id=$1 AND organization_id=$2 ORDER BY is_primary DESC,id LIMIT 1`, member.ID, tenant.ID).Scan(&profileID); err != nil {
		t.Fatalf("load tenant member profile: %v", err)
	}
	sessions := auth.NewSessionRepository(pool)
	singleSessionID := "single-session-" + uuid.NewString()
	if err := sessions.Create(ctx, models.AuthSession{ID: singleSessionID, UserID: member.ID, ProfileID: &profileID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("create single session: %v", err)
	}
	singleSessionKey := "tenant-member-session-delete-" + uuid.NewString()
	singleSessionPath := path + "/auth-sessions/" + singleSessionID
	revokeSingle := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, singleSessionPath, nil)
		req.Header.Set("Idempotency-Key", singleSessionKey)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}
	if firstRevoke := revokeSingle(); firstRevoke.Code != http.StatusNoContent {
		t.Fatalf("first session revoke = %d: %s", firstRevoke.Code, firstRevoke.Body.String())
	}
	otherSessionID := "other-session-" + uuid.NewString()
	if err := sessions.Create(ctx, models.AuthSession{ID: otherSessionID, UserID: member.ID, ProfileID: &profileID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("create other session: %v", err)
	}
	conflictingRequest := httptest.NewRequest(http.MethodDelete, path+"/auth-sessions/"+otherSessionID, nil)
	conflictingRequest.Header.Set("Idempotency-Key", singleSessionKey)
	conflictingResponse := httptest.NewRecorder()
	router.ServeHTTP(conflictingResponse, conflictingRequest)
	if conflictingResponse.Code != http.StatusConflict {
		t.Fatalf("same key for other session = %d: %s", conflictingResponse.Code, conflictingResponse.Body.String())
	}
	if valid, err := sessions.IsValid(ctx, otherSessionID); err != nil || !valid {
		t.Fatalf("other session after conflict valid=%v, error=%v", valid, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE auth_sessions SET revoked_at=NULL WHERE id=$1`, singleSessionID); err != nil {
		t.Fatalf("restore session after first revoke: %v", err)
	}
	if replayRevoke := revokeSingle(); replayRevoke.Code != http.StatusNoContent {
		t.Fatalf("replay session revoke = %d: %s", replayRevoke.Code, replayRevoke.Body.String())
	}
	if valid, err := sessions.IsValid(ctx, singleSessionID); err != nil || !valid {
		t.Fatalf("session after replay valid=%v, error=%v", valid, err)
	}

	allScopedID := "all-scoped-" + uuid.NewString()
	allAccountID := "all-account-" + uuid.NewString()
	if err := sessions.Create(ctx, models.AuthSession{ID: allScopedID, UserID: member.ID, ProfileID: &profileID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("create scoped all-session: %v", err)
	}
	if err := sessions.Create(ctx, models.AuthSession{ID: allAccountID, UserID: member.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("create account all-session: %v", err)
	}
	allKey := "tenant-member-sessions-delete-" + uuid.NewString()
	revokeAll := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, path+"/auth-sessions", nil)
		req.Header.Set("Idempotency-Key", allKey)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}
	if firstAll := revokeAll(); firstAll.Code != http.StatusNoContent {
		t.Fatalf("first all-session revoke = %d: %s", firstAll.Code, firstAll.Body.String())
	}
	if valid, err := sessions.IsValid(ctx, allScopedID); err != nil || valid {
		t.Fatalf("scoped session after revoke-all valid=%v, error=%v", valid, err)
	}
	if valid, err := sessions.IsValid(ctx, allAccountID); err != nil || !valid {
		t.Fatalf("account session after scoped revoke-all valid=%v, error=%v", valid, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE auth_sessions SET revoked_at=NULL WHERE id=$1`, allScopedID); err != nil {
		t.Fatalf("restore scoped session after revoke-all: %v", err)
	}
	if replayAll := revokeAll(); replayAll.Code != http.StatusNoContent {
		t.Fatalf("replay all-session revoke = %d: %s", replayAll.Code, replayAll.Body.String())
	}
	if valid, err := sessions.IsValid(ctx, allScopedID); err != nil || !valid {
		t.Fatalf("scoped session after replay-all valid=%v, error=%v", valid, err)
	}

	resetKey := "tenant-member-reset-" + uuid.NewString()
	resetRequest := func(password string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"password": password})
		req := httptest.NewRequest(http.MethodPost, path+"/reset-password", bytes.NewReader(body))
		req.Header.Set("Idempotency-Key", resetKey)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}
	firstPassword := "first-reset-password"
	firstReset := resetRequest(firstPassword)
	if firstReset.Code != http.StatusOK {
		t.Fatalf("first reset = %d: %s", firstReset.Code, firstReset.Body.String())
	}
	laterPassword := "later-reset-password"
	if _, err := memberService.ResetPassword(ctx, tenant.ID, member.ID, laterPassword); err != nil {
		t.Fatalf("change password after first reset: %v", err)
	}
	replayReset := resetRequest(firstPassword)
	if replayReset.Code != http.StatusOK || replayReset.Body.String() != firstReset.Body.String() {
		t.Fatalf("replay reset = %d %s; first = %d %s", replayReset.Code, replayReset.Body.String(), firstReset.Code, firstReset.Body.String())
	}
	got, err = memberService.Get(ctx, tenant.ID, member.ID)
	if err != nil || !auth.CheckPassword(&got, laterPassword) || auth.CheckPassword(&got, firstPassword) {
		t.Fatalf("password after replay preserved later mutation = %v, error = %v", auth.CheckPassword(&got, laterPassword), err)
	}

	deleteKey := "tenant-member-delete-" + uuid.NewString()
	deleteRequest := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		req.Header.Set("Idempotency-Key", deleteKey)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}
	if firstDelete := deleteRequest(); firstDelete.Code != http.StatusNoContent {
		t.Fatalf("first delete = %d: %s", firstDelete.Code, firstDelete.Body.String())
	}
	var replacementIncarnation uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO users (id,username,email,password_hash,role)
VALUES ($1,$2,$3,'x','user') RETURNING account_incarnation_id`, member.ID,
		"replacement-"+uuid.NewString(), uuid.NewString()+"@tenant-lifecycle.test").Scan(&replacementIncarnation); err != nil {
		t.Fatalf("create same-number replacement: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','user')`, tenant.ID, member.ID); err != nil {
		t.Fatalf("create replacement membership: %v", err)
	}
	var accessGroupID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, tenant.ID).Scan(&accessGroupID); err != nil {
		t.Fatalf("load tenant default access group: %v", err)
	}
	replacementProfileID := "replacement-profile-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id)
VALUES ($1,$2,'Replacement',$3,$4)`, replacementProfileID, member.ID, tenant.ID, accessGroupID); err != nil {
		t.Fatalf("create replacement profile: %v", err)
	}
	if err := sessions.Create(ctx, models.AuthSession{ID: singleSessionID, UserID: member.ID, ProfileID: &replacementProfileID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("create same-id replacement session: %v", err)
	}
	if replayRevoke := revokeSingle(); replayRevoke.Code != http.StatusNoContent {
		t.Fatalf("replacement replay session revoke = %d: %s", replayRevoke.Code, replayRevoke.Body.String())
	}
	if valid, err := sessions.IsValid(ctx, singleSessionID); err != nil || !valid {
		t.Fatalf("replacement session after replay valid=%v, error=%v", valid, err)
	}
	if replayDelete := deleteRequest(); replayDelete.Code != http.StatusNoContent {
		t.Fatalf("replay delete = %d: %s", replayDelete.Code, replayDelete.Body.String())
	}
	var replacementPresent bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND account_incarnation_id=$2)`, member.ID, replacementIncarnation).Scan(&replacementPresent); err != nil || !replacementPresent {
		t.Fatalf("replacement present = %v, error = %v", replacementPresent, err)
	}
}
