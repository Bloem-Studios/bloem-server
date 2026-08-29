package handlers_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

type lifecycleMembershipProvisioner struct{ store *tenancy.Store }

func (p lifecycleMembershipProvisioner) ProvisionDefaultMembership(ctx context.Context, accountID int, role string) error {
	_, err := p.store.ProvisionDefaultMembership(ctx, accountID, role)
	return err
}

func (p lifecycleMembershipProvisioner) ProvisionDefaultMembershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int, role string) (uuid.UUID, uuid.UUID, error) {
	membership, err := p.store.ProvisionDefaultMembershipInTransaction(ctx, tx, accountID, role)
	return membership.OrganizationID, membership.ID, err
}

type remainingLifecycleFixture struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	users    *auth.UserRepository
	provider *pgstore.PostgresProvider
	actor    *models.User
	orgID    uuid.UUID
	secret   []byte
}

func newRemainingLifecycleFixture(t *testing.T) remainingLifecycleFixture {
	t.Helper()
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
	actor, err := users.Create(ctx, models.CreateUserInput{Username: "remaining-actor-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test", Password: "password", Role: models.RoleAdmin})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	var orgID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&orgID); err != nil {
		t.Fatalf("default organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','admin')`, orgID, actor.ID); err != nil {
		t.Fatalf("actor membership: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor.ID) })
	return remainingLifecycleFixture{ctx: ctx, pool: pool, users: users, provider: pgstore.NewPostgresProvider(pool), actor: actor, orgID: orgID, secret: []byte("remaining-lifecycle-secret")}
}

func (f remainingLifecycleFixture) withClaims(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := &auth.Claims{UserID: f.actor.ID, AccountIncarnationID: f.actor.AccountIncarnationID.String(), Role: models.RoleAdmin}
		next.ServeHTTP(w, r.WithContext(apimw.SetClaims(r.Context(), claims)))
	})
}

func TestAdminCreateUserLifecycleReplayCreatesOneAccountGraph(t *testing.T) {
	f := newRemainingLifecycleFixture(t)
	h := handlers.NewAdminHandler(f.users, f.pool, f.provider)
	h.SetMembershipProvisioner(lifecycleMembershipProvisioner{store: tenancy.NewStore(f.pool)})
	h.SetLifecycleIdempotency(lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(f.pool), lifecycleidempotency.NewHMACKeyDigester(f.secret)), lifecycleidempotency.NewRequestDigester(f.secret))
	router := chi.NewRouter()
	router.Use(f.withClaims)
	router.Post("/api/v1/admin/users", h.HandleCreateUser)
	email := uuid.NewString() + "@lifecycle.test"
	t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM users WHERE email=$1`, email) })
	body := []byte(`{"username":"create-replay-` + uuid.NewString() + `","email":"` + email + `","password":"password","role":"user","create_default_profile":true}`)
	key := "account-create-" + uuid.NewString()
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", bytes.NewReader(body))
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	first, replay := request(), request()
	if first.Code != http.StatusCreated || replay.Code != first.Code || replay.Body.String() != first.Body.String() {
		t.Fatalf("create first/replay = %d %q / %d %q", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	var users, memberships, profiles int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM users WHERE email=$1`, email).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE u.email=$1`, email).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM user_profiles p JOIN users u ON u.id=p.user_id WHERE u.email=$1`, email).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if users != 1 || memberships != 1 || profiles != 1 {
		t.Fatalf("created graph counts = users %d memberships %d profiles %d", users, memberships, profiles)
	}
}

func TestAdminSettingLifecycleSetAndDeleteReplay(t *testing.T) {
	f := newRemainingLifecycleFixture(t)
	target, err := f.users.Create(f.ctx, models.CreateUserInput{Username: "setting-target-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test", Password: "password", Role: models.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.users.Delete(context.Background(), target.ID) })
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, f.orgID, target.ID); err != nil {
		t.Fatal(err)
	}
	profileID := uuid.NewString()
	store, err := f.provider.ForUser(f.ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProfile(f.ctx, userstore.Profile{ID: profileID, Name: "Settings"}); err != nil {
		t.Fatal(err)
	}
	contract, err := settingscontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	h := handlers.NewSettingValuesHandler(f.provider, contract)
	h.SetLifecycleIdempotency(lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(f.pool), lifecycleidempotency.NewHMACKeyDigester(f.secret)), lifecycleidempotency.NewRequestDigester(f.secret))
	router := chi.NewRouter()
	router.Use(f.withClaims)
	router.Put("/api/v1/admin/users/{id}/settings/values/{key}", h.HandleAdminSetUserSettingValue)
	router.Delete("/api/v1/admin/users/{id}/settings/values/{key}", h.HandleAdminDeleteUserSettingValue)
	path := "/api/v1/admin/users/" + strconv.Itoa(target.ID) + "/settings/values/playback.subtitle_mode?scope=profile&profile_id=" + profileID
	do := func(method, key string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	setKey := "setting-set-" + uuid.NewString()
	firstSet, replaySet := do(http.MethodPut, setKey, []byte(`{"value":"always"}`)), do(http.MethodPut, setKey, []byte(`{"value":"always"}`))
	if firstSet.Code != http.StatusOK || replaySet.Code != firstSet.Code || replaySet.Body.String() != firstSet.Body.String() {
		t.Fatalf("set first/replay = %d %q / %d %q", firstSet.Code, firstSet.Body.String(), replaySet.Code, replaySet.Body.String())
	}
	deleteKey := "setting-delete-" + uuid.NewString()
	firstDelete, replayDelete := do(http.MethodDelete, deleteKey, nil), do(http.MethodDelete, deleteKey, nil)
	if firstDelete.Code != http.StatusNoContent || replayDelete.Code != firstDelete.Code || replayDelete.Body.String() != firstDelete.Body.String() {
		t.Fatalf("delete first/replay = %d %q / %d %q", firstDelete.Code, firstDelete.Body.String(), replayDelete.Code, replayDelete.Body.String())
	}
}

func TestAdminImpersonationLifecycleReplayDoesNotCreateReplacementSession(t *testing.T) {
	f := newRemainingLifecycleFixture(t)
	target, err := f.users.Create(f.ctx, models.CreateUserInput{Username: "impersonation-target-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test", Password: "password", Role: models.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, f.orgID, target.ID); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionRepository(f.pool)
	original := models.AuthSession{ID: uuid.NewString(), UserID: f.actor.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := sessions.Create(f.ctx, original); err != nil {
		t.Fatal(err)
	}
	service := auth.NewService(nil, auth.NewJWTService("impersonation-test-secret", time.Hour, 24*time.Hour), sessions, f.users, auth.NewInviteCodeRepository(f.pool), nil, f.provider)
	h := handlers.NewAdminHandler(f.users, f.pool, f.provider)
	h.ImpersonationService = service
	h.SetLifecycleIdempotency(lifecycleidempotency.NewEncryptedCoordinator(lifecycleidempotency.NewPostgresStore(f.pool), f.secret), lifecycleidempotency.NewRequestDigester(f.secret))
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{UserID: f.actor.ID, AccountIncarnationID: f.actor.AccountIncarnationID.String(), Role: models.RoleAdmin, SessionID: original.ID}
			next.ServeHTTP(w, r.WithContext(apimw.SetClaims(r.Context(), claims)))
		})
	})
	router.Post("/api/v1/admin/users/{id}/impersonate", h.HandleImpersonateUser)
	key := "impersonate-" + uuid.NewString()
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+strconv.Itoa(target.ID)+"/impersonate", nil)
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	first := request()
	if first.Code != http.StatusOK {
		t.Fatalf("first impersonation = %d: %s", first.Code, first.Body.String())
	}
	if err := f.users.Delete(f.ctx, target.ID); err != nil {
		t.Fatalf("delete original target: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO users (id,username,email,password_hash,role) VALUES ($1,$2,$3,'x','user')`, target.ID, "replacement-"+uuid.NewString(), uuid.NewString()+"@lifecycle.test"); err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, f.orgID, target.ID); err != nil {
		t.Fatalf("replacement membership: %v", err)
	}
	replay := request()
	if replay.Code != first.Code || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay impersonation = %d %q, want %d %q", replay.Code, replay.Body.String(), first.Code, first.Body.String())
	}
	var sessionsForReplacement int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM auth_sessions WHERE user_id=$1 AND impersonator_user_id=$2`, target.ID, f.actor.ID).Scan(&sessionsForReplacement); err != nil {
		t.Fatal(err)
	}
	if sessionsForReplacement != 0 {
		t.Fatalf("replacement impersonation sessions = %d, want 0", sessionsForReplacement)
	}
}
