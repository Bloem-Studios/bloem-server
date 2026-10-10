package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

type invitationReceiptMemberships struct{ v1TenancyBootstrap }

func (b invitationReceiptMemberships) ProvisionMembershipInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int, role string) (uuid.UUID, uuid.UUID, error) {
	membership, err := b.store.ProvisionMembershipInTransaction(ctx, tx, organizationID, accountID, role)
	return membership.OrganizationID, membership.ID, err
}

// This exercises the production constructor: coordinator-only tests cannot
// detect an invitation handler wired to persist credential-bearing plaintext.
func TestBloemInvitationWiringEncryptsRetainedLoginResponse(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	ctx := context.Background()
	users := auth.NewUserRepository(pool)
	inviter, err := users.Create(ctx, models.CreateUserInput{
		Username: "receipt-inviter", Email: "receipt-inviter@example.test", Password: "test-password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := tenancy.NewStore(pool)
	if _, err := store.ActivateInitialOwnership(ctx, inviter.ID); err != nil {
		t.Fatal(err)
	}
	organization, err := store.DefaultOrganization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	token, tokenHash, err := invitations.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	repository := invitations.NewRepository(pool)
	if _, err := repository.CreateForOrganization(ctx, organization.ID, models.CreateInvitationInput{
		Email: "receipt-invitee@example.test", Role: models.RoleUser, InvitedBy: int64(inviter.ID), CreateProfile: true, ExpiresAt: time.Now().Add(time.Hour),
	}, tokenHash); err != nil {
		t.Fatal(err)
	}
	provider := pgstore.NewPostgresProvider(pool)
	accounts := auth.NewAccountProvisioner(users, provider)
	accounts.SetMembershipProvisioner(invitationReceiptMemberships{v1TenancyBootstrap{store: store}})
	sessions := auth.NewSessionRepository(pool)
	const secret = "invitation-wiring-test-secret"
	authService := auth.NewService(auth.NewLocalProvider(users, sessions), auth.NewJWTService(secret, time.Hour, 24*time.Hour), sessions, users, auth.NewInviteCodeRepository(pool), nil, provider)
	service := invitations.NewService(repository, users, accounts, authService, nil, nil, nil, "")
	handler := newBloemV1InvitationHandler(Dependencies{DB: pool, Config: &config.Config{Auth: config.AuthConfig{JWTSecret: secret}}}, service, nil)
	router := chi.NewRouter()
	router.Post("/api/v1/invitations/{token}/accept", handler.HandleAcceptInvitation)
	const key = "encrypted-invitation-wiring-key"
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/"+token+"/accept", bytes.NewBufferString(`{"password":"correct-horse-battery"}`))
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	first := request()
	if first.Code != http.StatusCreated {
		t.Fatalf("invitation accept status = %d", first.Code)
	}
	var login v1LoginEnvelope
	if err := json.Unmarshal(first.Body.Bytes(), &login); err != nil || login.AccessToken == "" || login.RefreshToken == "" {
		t.Fatal("invitation response did not issue a login token pair")
	}
	digest := lifecycleidempotency.NewHMACKeyDigester([]byte(secret))(key)
	var retained []byte
	if err := pool.QueryRow(ctx, `SELECT response_body FROM lifecycle_request_receipts WHERE idempotency_key_digest=$1`, digest[:]).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(retained, []byte(login.AccessToken)) || bytes.Contains(retained, []byte(login.RefreshToken)) || bytes.Equal(retained, first.Body.Bytes()) {
		t.Fatal("production invitation wiring persisted the login response in plaintext")
	}
	replay := request()
	if replay.Code != first.Code || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("encrypted invitation receipt did not replay the exact completed response")
	}
}
