package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/go-chi/chi/v5"
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
	if response.Header().Get("Idempotency-Replayed") != "" {
		t.Fatal("fresh refusal was marked as a receipt replay")
	}
}

func bloemAssertInvitationCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accounts, memberships, profiles, sessions, receipts int) {
	t.Helper()
	for _, check := range []struct {
		name, query string
		want        int
	}{
		{"accounts", `SELECT count(*) FROM users WHERE email='invitee@example.test'`, accounts},
		{"memberships", `SELECT count(*) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE u.email='invitee@example.test'`, memberships},
		{"profiles", `SELECT count(*) FROM user_profiles p JOIN users u ON u.id=p.user_id WHERE u.email='invitee@example.test'`, profiles},
		{"sessions", `SELECT count(*) FROM auth_sessions s JOIN users u ON u.id=s.user_id WHERE u.email='invitee@example.test'`, sessions},
		{"receipts", `SELECT count(*) FROM lifecycle_request_receipts WHERE route_id='invitation.accept'`, receipts},
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

func TestBloemInvitationAcceptFreshRequestRefusedButReceiptReplays(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	pool := newInvitationLifecycleDatabase(t, ctx)
	handler, token, organizationID := newInvitationLifecycleFixture(t, ctx, pool, lifecycleidempotency.NewPostgresStore(pool))
	first := bloemAcceptInvitation(ctx, handler, token, "bloem-accept-original")
	if first.Code != http.StatusCreated || first.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("first accept status = %d, replay header present = %v", first.Code, first.Header().Get("Idempotency-Replayed") != "")
	}
	for _, key := range []string{"bloem-accept-fresh", ""} {
		bloemAssertInvitationRefusal(t, bloemAcceptInvitation(ctx, handler, token, key), http.StatusNotFound, "not_found")
	}
	// Eligibility must remain behind receipt lookup even after fresh refusals.
	replay := bloemAcceptInvitation(ctx, handler, token, "bloem-accept-original")
	if replay.Code != http.StatusCreated || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) {
		t.Fatalf("receipt replay status = %d, exact body match = %v", replay.Code, bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()))
	}
	if replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("receipt replay header missing")
	}
	bloemAssertInvitationCounts(t, ctx, pool, 1, 1, 1, 1, 1)
	var accepted, completed bool
	if err := pool.QueryRow(ctx, `SELECT i.accepted_at IS NOT NULL AND i.accepted_user_id=u.id AND m.organization_id=$2 AND p.organization_id=$2
FROM invitations i JOIN users u ON u.email=i.email
JOIN organization_memberships m ON m.account_id=u.id
JOIN user_profiles p ON p.user_id=u.id WHERE i.token_hash=$1`, invitations.HashToken(token), organizationID).Scan(&accepted); err != nil || !accepted {
		t.Fatalf("accepted account and organization binding = %v, err = %v", accepted, err)
	}
	if err := pool.QueryRow(ctx, `SELECT state='completed' AND response_status=201 FROM lifecycle_request_receipts WHERE route_id='invitation.accept'`).Scan(&completed); err != nil || !completed {
		t.Fatalf("completed receipt = %v, err = %v", completed, err)
	}
}

func TestBloemInvitationAcceptIneligibleV1Contract(t *testing.T) {
	for _, state := range []string{"unknown", "expired", "revoked"} {
		t.Run(state, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			pool := newInvitationLifecycleDatabase(t, ctx)
			handler, token, _ := newInvitationLifecycleFixture(t, ctx, pool, lifecycleidempotency.NewPostgresStore(pool))
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
			bloemAssertInvitationCounts(t, ctx, pool, 0, 0, 0, 0, 0)
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
	pool := newInvitationLifecycleDatabase(t, ctx)
	handler, token, _ := newInvitationLifecycleFixture(t, ctx, pool, lifecycleidempotency.NewPostgresStore(pool))
	users := auth.NewUserRepository(pool)
	if _, err := users.Create(ctx, models.CreateUserInput{Username: "invitee@example.test", Email: "invitee@example.test", Password: "existing-account-password", Role: models.RoleUser}); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	pair, created, acceptErr := handler.service.AcceptInTransaction(ctx, tx, invitations.HashToken(token), "correct-horse-battery", "", "")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(acceptErr, invitations.ErrNotClaimable) || pair != nil || created.User != nil {
		t.Fatalf("duplicate account: error = %v, credentials present = %v, account present = %v", acceptErr, pair != nil, created.User != nil)
	}
	bloemAssertInvitationRefusal(t, bloemAcceptInvitation(ctx, handler, token, "bloem-duplicate-account"), http.StatusConflict, "already_used")
	bloemAssertInvitationCounts(t, ctx, pool, 1, 1, 0, 0, 0)
	var pending bool
	if err := pool.QueryRow(ctx, `SELECT accepted_at IS NULL AND accepted_user_id IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp() FROM invitations WHERE token_hash=$1`, invitations.HashToken(token)).Scan(&pending); err != nil || !pending {
		t.Fatalf("duplicate-account invitation remains pending = %v, err = %v", pending, err)
	}
}
