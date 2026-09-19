package executor

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/jackc/pgx/v5"
)

func TestBloemScenarioAccountPrerequisites(t *testing.T) {
	e := New(t)
	if !e.HasDatabase() {
		t.Fatal(DatabaseEnv + " is required for account fixture regression coverage")
	}
	u := e.users[fixtureMember]
	for _, token := range []string{e.accessToken(fixtureMember), e.refreshToken(fixtureMember)} {
		claims, err := e.jwt.ValidateToken(token)
		if err != nil {
			t.Fatal(err)
		}
		if claims.UserID != u.ID || claims.SessionID != e.sessions[fixtureMember] || claims.AccountIncarnationID != u.AccountIncarnationID.String() || claims.AuthMethod != "account" {
			t.Fatal("fixture token is not bound to its account and existing session")
		}
	}
	users := auth.NewUserRepository(e.pool)
	accounts := auth.NewAccountProvisioner(users, e.stores)
	accounts.SetMembershipProvisioner(bloemFixtureMemberships{store: tenancy.NewStore(e.pool)})
	service := invitations.NewService(invitations.NewRepository(e.pool), users, accounts, e.auth, nil, e.settings, "")
	tx, err := e.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	pair, created, err := service.AcceptInTransaction(t.Context(), tx, hashInvite(inviteToken), "fixture-accepted-password", "fixture", "127.0.0.1")
	if err != nil {
		t.Fatalf("fixture transactional invitation acceptance: %v", err)
	}
	if pair == nil || created.User == nil || created.ProfileID == "" {
		t.Fatal("transactional invitation fixture did not create its account, profile and session")
	}
}
