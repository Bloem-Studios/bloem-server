package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func bloemAcceptInvitation(ctx context.Context, handler *InvitationHandler, token, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/"+token+"/accept", bytes.NewBufferString(`{"password":"correct-horse-battery"}`)).WithContext(ctx)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Post("/api/v1/invitations/{token}/accept", handler.HandleAcceptInvitation)
	router.ServeHTTP(response, req)
	return response
}

func bloemAssertInvitationRefusal(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("accept status = %d, want %d", response.Code, status)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal("accept refusal is not JSON")
	}
	if body.Error != code {
		t.Fatalf("accept error code = %q, want %q", body.Error, code)
	}
}

func bloemAssertInvitationCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accounts, memberships, profiles, sessions int) {
	t.Helper()
	for _, check := range []struct {
		name, query string
		want        int
	}{
		{"accounts", `SELECT count(*) FROM users WHERE email='invitee@example.test'`, accounts},
		{"memberships", `SELECT count(*) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE u.email='invitee@example.test'`, memberships},
		{"profiles", `SELECT count(*) FROM user_profiles p JOIN users u ON u.id=p.user_id WHERE u.email='invitee@example.test'`, profiles},
		{"sessions", `SELECT count(*) FROM auth_sessions s JOIN users u ON u.id=s.user_id WHERE u.email='invitee@example.test'`, sessions},
	} {
		var got int
		if err := pool.QueryRow(ctx, check.query).Scan(&got); err != nil {
			t.Fatalf("query %s: %v", check.name, err)
		}
		if got != check.want {
			t.Fatalf("%s count = %d, want %d", check.name, got, check.want)
		}
	}
}

func TestBloemInvitationAcceptBindsTheOrganizationOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	pool := newInvitationTestDatabase(t, ctx)
	handler, token, organizationID := newInvitationTestFixture(t, ctx, pool)
	if first := bloemAcceptInvitation(ctx, handler, token, ""); first.Code != http.StatusCreated {
		t.Fatalf("first accept status = %d: %s", first.Code, first.Body.String())
	}
	// A consumed invitation answers not_found, with or without a retry key.
	for _, key := range []string{"bloem-accept-retry", ""} {
		bloemAssertInvitationRefusal(t, bloemAcceptInvitation(ctx, handler, token, key), http.StatusNotFound, "not_found")
	}
	bloemAssertInvitationCounts(t, ctx, pool, 1, 1, 1, 1)
	var accepted bool
	if err := pool.QueryRow(ctx, `SELECT i.accepted_at IS NOT NULL AND i.accepted_user_id=u.id AND m.organization_id=$2 AND p.organization_id=$2
FROM invitations i JOIN users u ON u.email=i.email
JOIN organization_memberships m ON m.account_id=u.id
JOIN user_profiles p ON p.user_id=u.id WHERE i.token_hash=$1`, invitations.HashToken(token), organizationID).Scan(&accepted); err != nil || !accepted {
		t.Fatalf("accepted account and organization binding = %v, err = %v", accepted, err)
	}
}

func TestBloemInvitationAcceptIneligibleV1Contract(t *testing.T) {
	for _, state := range []string{"unknown", "expired", "revoked"} {
		t.Run(state, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			pool := newInvitationTestDatabase(t, ctx)
			handler, token, _ := newInvitationTestFixture(t, ctx, pool)
			hash := invitations.HashToken(token)
			switch state {
			case "unknown":
				var err error
				token, _, err = invitations.NewToken()
				if err != nil {
					t.Fatal(err)
				}
			case "expired":
				if _, err := pool.Exec(ctx, `UPDATE invitations SET expires_at=clock_timestamp()-interval '1 hour' WHERE token_hash=$1`, hash); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if _, err := pool.Exec(ctx, `UPDATE invitations SET revoked_at=clock_timestamp() WHERE token_hash=$1`, hash); err != nil {
					t.Fatal(err)
				}
			}
			bloemAssertInvitationRefusal(t, bloemAcceptInvitation(ctx, handler, token, "bloem-ineligible"), http.StatusNotFound, "not_found")
			bloemAssertInvitationCounts(t, ctx, pool, 0, 0, 0, 0)
			var untouched bool
			if err := pool.QueryRow(ctx, `SELECT accepted_at IS NULL AND accepted_user_id IS NULL FROM invitations WHERE token_hash=$1`, hash).Scan(&untouched); err != nil || !untouched {
				t.Fatalf("invitation remains unclaimed = %v, err = %v", untouched, err)
			}
		})
	}
}

func TestBloemInvitationAcceptDuplicateAccountRemainsConflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	pool := newInvitationTestDatabase(t, ctx)
	handler, token, _ := newInvitationTestFixture(t, ctx, pool)
	users := auth.NewUserRepository(pool)
	if _, err := users.Create(ctx, models.CreateUserInput{Username: "invitee@example.test", Email: "invitee@example.test", Password: "existing-account-password", Role: models.RoleUser}); err != nil {
		t.Fatal(err)
	}
	bloemAssertInvitationRefusal(t, bloemAcceptInvitation(ctx, handler, token, "bloem-duplicate-account"), http.StatusConflict, "already_used")
	bloemAssertInvitationCounts(t, ctx, pool, 1, 1, 0, 0)
	var pending bool
	if err := pool.QueryRow(ctx, `SELECT accepted_at IS NULL AND accepted_user_id IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp() FROM invitations WHERE token_hash=$1`, invitations.HashToken(token)).Scan(&pending); err != nil || !pending {
		t.Fatalf("duplicate-account invitation remains pending = %v, err = %v", pending, err)
	}
}

func newInvitationTestFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (*InvitationHandler, string, uuid.UUID) {
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
	// The invited organization is the inviter's one organization: drop the
	// default-organization link account creation gave it.
	if _, err := pool.Exec(ctx, `DELETE FROM organization_memberships WHERE account_id=$1`, inviter.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET access_group_id=NULL WHERE id=$1`, inviter.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','admin')`, organizationID, inviter.ID); err != nil {
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
	accounts := auth.NewAccountProvisioner(users, pgstore.NewPostgresProvider(pool))
	sessions := auth.NewSessionRepository(pool)
	authService := auth.NewService(auth.NewLocalProvider(users, sessions), auth.NewJWTService("invitation-test-secret", time.Hour, 24*time.Hour), sessions, users, auth.NewInviteCodeRepository(pool), nil, pgstore.NewPostgresProvider(pool))
	service := invitations.NewService(repository, users, accounts, authService, nil, nil, nil, "")
	return NewInvitationHandler(service), token, organizationID
}

func newInvitationTestDatabase(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "bloem_invite_" + hex.EncodeToString(random[:])
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
	return pool
}
