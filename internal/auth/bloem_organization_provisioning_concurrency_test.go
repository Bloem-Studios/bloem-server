package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// Fixture secrets for the provisioning router; any fixed values work.
const (
	provisioningJWTSecret = "initial-setup-atomic-fixture-jwt-secret-000000000000"
	provisioningMasterKey = "initial-setup-atomic-fixture-master-key-0000000000"
)

// Two accounts created at once into one organization both commit, each with
// its one membership: the organization is locked before any account row, so
// no transaction waits to upgrade a lock its membership's foreign key holds.
func TestBloemInvitationOrganizationProvisioningConcurrent(t *testing.T) {
	pool := bloemAccountTransactionDatabase(t)
	organizationID, ownerID := bloemConcurrentOrganization(t, pool, true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg, err := config.LoadFromDB(map[string]string{
		"auth.jwt_secret":             provisioningJWTSecret,
		"jellyfin_compat.server_name": "Silo Fixture",
		"jellyfin_compat.server_id":   "00000000-0000-4000-8000-0000000000f2",
	})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secret.New([]byte(provisioningMasterKey))
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouter(api.Dependencies{
		Config: cfg, AppContext: ctx, DB: pool, SecretCipher: cipher,
		ClientIPResolver: clientip.NewResolver(nil), NodeID: "concurrency-fixture",
		PublicURL: "https://silo.example.test", UserStoreProvider: pgstore.NewPostgresProvider(pool),
	})

	var tokens [2]string
	var invitationIDs [2]int64
	emails := [2]string{"first-invitee@example.test", "second-invitee@example.test"}
	for i := range tokens {
		token, hash, err := invitations.NewToken()
		if err != nil {
			t.Fatal("create invitation token failed")
		}
		invitation, err := invitations.NewRepository(pool).CreateForOrganization(ctx, organizationID, models.CreateInvitationInput{
			Email: emails[i], Role: models.RoleUser, InvitedBy: int64(ownerID),
			CreateProfile: true, ExpiresAt: time.Now().Add(time.Hour),
		}, hash)
		if err != nil {
			t.Fatalf("store invitation: %s", bloemProvisioningError(err))
		}
		tokens[i], invitationIDs[i] = token, invitation.ID
	}

	outcomes := bloemRunConcurrentProvisioningPair(t, func(ctx context.Context, i int) bloemProvisioningOutcome {
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/invitations/"+tokens[i]+"/accept",
			bytes.NewBufferString(`{"password":"concurrent-invitation-password"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "BloemProvisioningConcurrency/1")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		outcome := bloemProvisioningOutcome{index: i, status: recorder.Code}
		if recorder.Code == http.StatusCreated {
			var reply struct {
				AccessToken  string           `json:"access_token"`
				RefreshToken string           `json:"refresh_token"`
				User         struct{ ID int } `json:"user"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
				outcome.err = errors.New("decode invitation response failed")
			} else if reply.AccessToken == "" || reply.RefreshToken == "" || reply.User.ID <= 0 {
				outcome.err = errors.New("invitation response lacks account or session tokens")
			}
			outcome.accountID = reply.User.ID
		}
		return outcome
	})
	for _, outcome := range outcomes {
		if outcome.err != nil || outcome.status != http.StatusCreated {
			t.Fatalf("invitation %d: status=%d error=%s, want 201", outcome.index, outcome.status, bloemProvisioningError(outcome.err))
		}
		bloemAssertProvisionedAccount(t, pool, emails[outcome.index], organizationID, outcome.accountID)
		var accepted, profile, session int
		if err := pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM invitations WHERE id=$1 AND organization_id=$2
			 AND accepted_user_id=$3 AND accepted_at IS NOT NULL AND revoked_at IS NULL),
			(SELECT count(*) FROM user_profiles WHERE user_id=$3 AND organization_id=$2),
			(SELECT count(*) FROM auth_sessions WHERE user_id=$3 AND revoked_at IS NULL)`,
			invitationIDs[outcome.index], organizationID, outcome.accountID).
			Scan(&accepted, &profile, &session); err != nil {
			t.Fatal(err)
		}
		if accepted != 1 || profile != 1 || session != 1 {
			t.Fatalf("invitation %d committed accepted/profile/session counts=%d/%d/%d, want 1/1/1",
				outcome.index, accepted, profile, session)
		}
	}
}

func TestBloemDefaultOrganizationProvisioningConcurrent(t *testing.T) {
	pool := bloemAccountTransactionDatabase(t)
	organizationID, _ := bloemConcurrentOrganization(t, pool, false)
	accounts := auth.NewAccountProvisioner(auth.NewUserRepository(pool), pgstore.NewPostgresProvider(pool))
	emails := [2]string{"first-account@example.test", "second-account@example.test"}
	outcomes := bloemRunConcurrentProvisioningPair(t, func(ctx context.Context, i int) bloemProvisioningOutcome {
		outcome := bloemProvisioningOutcome{index: i}
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			outcome.err = err
			return outcome
		}
		defer func() { _ = tx.Rollback(ctx) }()
		created, err := accounts.CreateAccountInTransaction(ctx, tx, auth.CreateAccountInput{
			User: models.CreateUserInput{Username: emails[i], Email: emails[i], Password: "concurrent-account-password", Role: models.RoleUser},
		})
		if err == nil {
			// Commit each caller immediately; waiting for its peer before commit
			// would itself deadlock the correctly serialized organization locks.
			err = tx.Commit(ctx)
			outcome.accountID = created.ID
		}
		outcome.err = err
		return outcome
	})
	for _, outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("default account %d: %s", outcome.index, bloemProvisioningError(outcome.err))
		}
		bloemAssertProvisionedAccount(t, pool, emails[outcome.index], organizationID, outcome.accountID)
	}
}

type bloemProvisioningOutcome struct {
	index, accountID, status int
	err                      error
}

func bloemRunConcurrentProvisioningPair(t *testing.T, run func(context.Context, int) bloemProvisioningOutcome) [2]bloemProvisioningOutcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	start := make(chan struct{})
	results := make(chan bloemProvisioningOutcome, 2)
	for i := range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- run(ctx, i)
		}()
	}
	close(start)
	var outcomes [2]bloemProvisioningOutcome
	for i := range outcomes {
		select {
		case outcomes[i] = <-results:
		case <-ctx.Done():
			t.Fatal("concurrent account provisioning did not finish")
		}
	}
	if outcomes[0].accountID != 0 && outcomes[0].accountID == outcomes[1].accountID {
		t.Fatal("concurrent provisioning must create distinct accounts")
	}
	return outcomes
}

func bloemConcurrentOrganization(t *testing.T, pool *pgxpool.Pool, nonDefault bool) (uuid.UUID, int) {
	t.Helper()
	ctx := t.Context()
	tenants := tenancy.NewStore(pool)
	owner, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{
		Username: "owner", Email: "owner@example.test", Password: "fixture-password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.ProvisionDefaultMembership(ctx, owner.ID, models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.ActivateInitialOwnership(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	organization, err := tenants.DefaultOrganization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !nonDefault {
		return organization.ID, owner.ID
	}
	organizationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,slug,name,status,owner_account_id)
		VALUES ($1,$2,'Invited Organization','active',$3)`, organizationID, organizationID.String(), owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_groups (organization_id,name,is_default) VALUES ($1,'Default',true)`, organizationID); err != nil {
		t.Fatal(err)
	}
	return organizationID, owner.ID
}

func bloemAssertProvisionedAccount(t *testing.T, pool *pgxpool.Pool, email string, organizationID uuid.UUID, accountID int) {
	t.Helper()
	var accounts, memberships, intended int
	if err := pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM users WHERE email=$1 AND id=$2),
		(SELECT count(*) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE u.email=$1),
		(SELECT count(*) FROM organization_memberships WHERE account_id=$2 AND organization_id=$3
		 AND status='active' AND legacy_role='user')`, email, accountID, organizationID).
		Scan(&accounts, &memberships, &intended); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 || memberships != 1 || intended != 1 {
		t.Fatalf("committed account/membership/intended counts=%d/%d/%d, want 1/1/1", accounts, memberships, intended)
	}
}

// SQLSTATE diagnoses lock failures without printing connection or token data.
func bloemProvisioningError(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return "SQLSTATE " + pgErr.Code
	}
	return fmt.Sprintf("%T", err)
}
