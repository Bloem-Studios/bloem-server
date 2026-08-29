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

	handler := handlers.NewAdminTenantMembersHandler(memberService, nil)
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
	if replayDelete := deleteRequest(); replayDelete.Code != http.StatusNoContent {
		t.Fatalf("replay delete = %d: %s", replayDelete.Code, replayDelete.Body.String())
	}
	var replacementPresent bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND account_incarnation_id=$2)`, member.ID, replacementIncarnation).Scan(&replacementPresent); err != nil || !replacementPresent {
		t.Fatalf("replacement present = %v, error = %v", replacementPresent, err)
	}
}
