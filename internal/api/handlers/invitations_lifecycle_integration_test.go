package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type invitationLifecycleIdentity string

func (id invitationLifecycleIdentity) Resolve(context.Context) (string, error) {
	return string(id), nil
}

type invitationLifecycleMemberships struct{ store *tenancy.Store }

func (a invitationLifecycleMemberships) ProvisionDefaultMembership(ctx context.Context, accountID int, role string) error {
	_, err := a.store.ProvisionDefaultMembership(ctx, accountID, role)
	return err
}

func (a invitationLifecycleMemberships) ProvisionDefaultMembershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int, role string) (uuid.UUID, uuid.UUID, error) {
	membership, err := a.store.ProvisionDefaultMembershipInTransaction(ctx, tx, accountID, role)
	return membership.OrganizationID, membership.ID, err
}

func (a invitationLifecycleMemberships) ProvisionMembershipInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int, role string) (uuid.UUID, uuid.UUID, error) {
	membership, err := a.store.ProvisionMembershipInTransaction(ctx, tx, organizationID, accountID, role)
	return membership.OrganizationID, membership.ID, err
}

type invitationFailingCompleteStore struct {
	lifecycleidempotency.Store
	err error
}

func TestInvitationAcceptSubjectDigestBindsServerAndExactTokenDigest(t *testing.T) {
	secret := []byte("subject-secret")
	base := invitationAcceptSubjectDigest(secret, "server-a", invitations.HashToken("Token"))
	if base == invitationAcceptSubjectDigest(secret, "server-b", invitations.HashToken("Token")) {
		t.Fatal("subject digest did not bind the stable server identity")
	}
	if base == invitationAcceptSubjectDigest(secret, "server-a", invitations.HashToken("token")) {
		t.Fatal("subject digest normalized the exact path token")
	}
}

func (s invitationFailingCompleteStore) Complete(context.Context, pgx.Tx, lifecycleidempotency.Digest, lifecycleidempotency.Result) error {
	return s.err
}

func TestInvitationAcceptLifecycleReplaysConsumedTokenExactly(t *testing.T) {
	ctx := context.Background()
	pool := newInvitationLifecycleDatabase(t, ctx)
	handler, token, organizationID := newInvitationLifecycleFixture(t, ctx, pool, lifecycleidempotency.NewPostgresStore(pool))

	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/"+token+"/accept", bytes.NewBufferString(`{"password":"correct-horse-battery"}`))
		req.Header.Set("Idempotency-Key", "invitation-accept-replay-key")
		recorder := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/invitations/{token}/accept", handler.HandleAcceptInvitation)
		router.ServeHTTP(recorder, req)
		return recorder
	}

	first := request()
	if first.Code != http.StatusCreated {
		t.Fatalf("first accept = %d: %s", first.Code, first.Body.String())
	}
	replay := request()
	if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d %q, want exact %d %q", replay.Code, replay.Body.String(), first.Code, first.Body.String())
	}
	if replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("replay did not advertise Idempotency-Replayed")
	}

	for name, query := range map[string]string{
		"account":    `SELECT count(*) FROM users WHERE email='invitee@example.test'`,
		"membership": `SELECT count(*) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE u.email='invitee@example.test' AND m.organization_id='` + organizationID.String() + `'`,
		"profile":    `SELECT count(*) FROM user_profiles p JOIN users u ON u.id=p.user_id WHERE u.email='invitee@example.test' AND p.organization_id='` + organizationID.String() + `'`,
		"session":    `SELECT count(*) FROM auth_sessions s JOIN users u ON u.id=s.user_id WHERE u.email='invitee@example.test'`,
		"receipt":    `SELECT count(*) FROM lifecycle_request_receipts WHERE route_id='invitation.accept'`,
	} {
		var count int
		if err := pool.QueryRow(ctx, query).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count = %d, err = %v", name, count, err)
		}
	}
}

func TestInvitationAcceptLifecycleRollsBackOnStoredResponseFailure(t *testing.T) {
	ctx := context.Background()
	pool := newInvitationLifecycleDatabase(t, ctx)
	wantErr := errors.New("store response failed")
	store := invitationFailingCompleteStore{Store: lifecycleidempotency.NewPostgresStore(pool), err: wantErr}
	handler, token, _ := newInvitationLifecycleFixture(t, ctx, pool, store)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/"+token+"/accept", bytes.NewBufferString(`{"password":"correct-horse-battery"}`))
	req.Header.Set("Idempotency-Key", "invitation-accept-rollback-key")
	recorder := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Post("/api/v1/invitations/{token}/accept", handler.HandleAcceptInvitation)
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("accept = %d: %s", recorder.Code, recorder.Body.String())
	}

	for name, query := range map[string]string{
		"account": `SELECT count(*) FROM users WHERE email='invitee@example.test'`,
		"session": `SELECT count(*) FROM auth_sessions s JOIN users u ON u.id=s.user_id WHERE u.email='invitee@example.test'`,
		"receipt": `SELECT count(*) FROM lifecycle_request_receipts WHERE route_id='invitation.accept'`,
	} {
		var count int
		if err := pool.QueryRow(ctx, query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s survived rollback: count=%d err=%v", name, count, err)
		}
	}
	var accepted bool
	if err := pool.QueryRow(ctx, `SELECT accepted_at IS NOT NULL FROM invitations WHERE token_hash=$1`, invitations.HashToken(token)).Scan(&accepted); err != nil || accepted {
		t.Fatalf("invitation accepted after rollback = %v, err = %v", accepted, err)
	}
}

func newInvitationLifecycleFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store lifecycleidempotency.Store) (*InvitationHandler, string, uuid.UUID) {
	t.Helper()
	users := auth.NewUserRepository(pool)
	inviter, err := users.Create(ctx, models.CreateUserInput{Username: "inviter", Email: "inviter@example.test", Password: "password", Role: models.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	organizationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,slug,name,status,owner_account_id) VALUES ($1,$2,'Invited Organization','active',$3)`, organizationID, "invited-"+organizationID.String(), inviter.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
SELECT $1,$2,'active','admin'
WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL
ON CONFLICT (organization_id, account_id) DO UPDATE
SET status = EXCLUDED.status, legacy_role = EXCLUDED.legacy_role`, organizationID, inviter.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_groups (organization_id,name,is_default) VALUES ($1,$2,true)`, organizationID, "Default "+organizationID.String()); err != nil {
		t.Fatal(err)
	}
	token, tokenHash, err := invitations.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	repository := invitations.NewRepository(pool)
	if _, err := repository.CreateForOrganization(ctx, organizationID, models.CreateInvitationInput{
		Email: "invitee@example.test", Role: models.RoleUser, InvitedBy: int64(inviter.ID), CreateProfile: true, ExpiresAt: time.Now().Add(time.Hour),
	}, tokenHash); err != nil {
		t.Fatal(err)
	}

	memberships := invitationLifecycleMemberships{store: tenancy.NewStore(pool)}
	accounts := auth.NewAccountProvisioner(users, pgstore.NewPostgresProvider(pool))
	accounts.SetMembershipProvisioner(memberships)
	sessions := auth.NewSessionRepository(pool)
	authService := auth.NewService(auth.NewLocalProvider(users, sessions), auth.NewJWTService("invitation-test-secret", time.Hour, 24*time.Hour), sessions, users, auth.NewInviteCodeRepository(pool), nil, pgstore.NewPostgresProvider(pool))
	service := invitations.NewService(repository, users, accounts, authService, nil, nil, "")
	handler := NewInvitationHandler(service)
	secret := []byte("invitation-test-secret")
	handler.SetLifecycleIdempotency(
		lifecycleidempotency.NewCoordinator(store, lifecycleidempotency.NewHMACKeyDigester(secret)),
		lifecycleidempotency.NewRequestDigester(secret),
		invitationLifecycleIdentity("stable-server-id"),
		secret,
	)
	return handler, token, organizationID
}

func newInvitationLifecycleDatabase(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "bloem_invite_lifecycle_" + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	testConfig := adminConfig.Copy()
	testConfig.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
	})
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	// A freshly migrated database is in the compatibility phase, which freezes
	// every policy write including the membership a new account is given.
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}
	return pool
}
