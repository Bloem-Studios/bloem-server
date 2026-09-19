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

// With FOR UPDATE on the organization, both transactions retain the seeded
// membership's FK KEY SHARE lock while waiting to upgrade it: one must abort.
// The barrier observes both real inserts before either real provisioner runs.
func TestBloemInvitationOrganizationProvisioningConcurrent(t *testing.T) {
	pool := bloemAccountTransactionDatabase(t)
	tenants, organizationID, ownerID := bloemConcurrentOrganization(t, pool, true)
	memberships := newBloemSeededMembershipBarrier(tenants, organizationID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg, err := config.LoadFromDB(map[string]string{
		"auth.jwt_secret":             atomicJWTSecret,
		"jellyfin_compat.server_name": "Silo Fixture",
		"jellyfin_compat.server_id":   "00000000-0000-4000-8000-0000000000f2",
	})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secret.New([]byte(atomicMasterKey))
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouter(api.Dependencies{
		Config: cfg, AppContext: ctx, DB: pool, SecretCipher: cipher,
		ClientIPResolver: clientip.NewResolver(nil), NodeID: "concurrency-fixture",
		PublicURL: "https://silo.example.test", UserStoreProvider: pgstore.NewPostgresProvider(pool),
		MembershipProvisioner: memberships,
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

	seeds, outcomes := bloemRunSeededProvisioningPair(t, memberships, func(ctx context.Context, i int) bloemProvisioningOutcome {
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/invitations/"+tokens[i]+"/accept",
			bytes.NewBufferString(`{"password":"concurrent-invitation-password"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", fmt.Sprintf("bloem-concurrent-invitation-%d", i))
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
		seed := seeds[outcome.accountID]
		bloemAssertProvisionedAccount(t, pool, emails[outcome.index], organizationID, seed)
		var accepted, profile, session, receipt int
		if err := pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM invitations WHERE id=$1 AND organization_id=$2
			 AND accepted_user_id=$3 AND accepted_at IS NOT NULL AND revoked_at IS NULL),
			(SELECT count(*) FROM user_profiles WHERE user_id=$3 AND organization_id=$2),
			(SELECT count(*) FROM auth_sessions WHERE user_id=$3 AND revoked_at IS NULL),
			(SELECT count(*) FROM lifecycle_request_receipts r
			 JOIN lifecycle_request_receipt_targets target USING (idempotency_key_digest)
			 JOIN users u ON u.id=target.account_id AND u.account_incarnation_id=target.account_incarnation_id
			 JOIN user_profiles p ON p.id=target.profile_id AND p.user_id=u.id AND p.organization_id=target.organization_id
			 WHERE r.route_id='invitation.accept' AND r.state='completed' AND r.response_status=201
			 AND target.account_id=$3 AND target.organization_id=$2 AND target.membership_id=$4)`,
			invitationIDs[outcome.index], organizationID, outcome.accountID, seed.membershipID).
			Scan(&accepted, &profile, &session, &receipt); err != nil {
			t.Fatal(err)
		}
		if accepted != 1 || profile != 1 || session != 1 || receipt != 1 {
			t.Fatalf("invitation %d committed accepted/profile/session/receipt counts=%d/%d/%d/%d, want 1/1/1/1",
				outcome.index, accepted, profile, session, receipt)
		}
	}
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_request_receipts WHERE route_id='invitation.accept'`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 2 {
		t.Fatalf("invitation receipt count=%d, want exactly 2", receipts)
	}
}

func TestBloemDefaultOrganizationProvisioningConcurrent(t *testing.T) {
	pool := bloemAccountTransactionDatabase(t)
	tenants, organizationID, _ := bloemConcurrentOrganization(t, pool, false)
	memberships := newBloemSeededMembershipBarrier(tenants, organizationID)
	accounts := auth.NewAccountProvisioner(auth.NewUserRepository(pool), pgstore.NewPostgresProvider(pool))
	accounts.SetMembershipProvisioner(memberships)
	emails := [2]string{"first-account@example.test", "second-account@example.test"}
	seeds, outcomes := bloemRunSeededProvisioningPair(t, memberships, func(ctx context.Context, i int) bloemProvisioningOutcome {
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
			outcome.accountID = created.User.ID
		}
		outcome.err = err
		return outcome
	})
	for _, outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("default account %d: %s", outcome.index, bloemProvisioningError(outcome.err))
		}
		bloemAssertProvisionedAccount(t, pool, emails[outcome.index], organizationID, seeds[outcome.accountID])
	}
}

type bloemMembershipSeed struct {
	accountID    int
	membershipID uuid.UUID
	backendPID   int
}

type bloemSeededMembershipBarrier struct {
	store          *tenancy.Store
	organizationID uuid.UUID
	arrived        chan bloemMembershipSeed
	release        chan struct{}
}

func newBloemSeededMembershipBarrier(store *tenancy.Store, organizationID uuid.UUID) *bloemSeededMembershipBarrier {
	return &bloemSeededMembershipBarrier{store: store, organizationID: organizationID,
		arrived: make(chan bloemMembershipSeed, 2), release: make(chan struct{})}
}

func (b *bloemSeededMembershipBarrier) ProvisionDefaultMembership(ctx context.Context, accountID int, role string) error {
	_, err := b.store.ProvisionDefaultMembership(ctx, accountID, role)
	return err
}

func (b *bloemSeededMembershipBarrier) ProvisionDefaultMembershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int, role string) (uuid.UUID, uuid.UUID, error) {
	if err := b.waitForPeer(ctx, tx, b.organizationID, accountID, role); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	m, err := b.store.ProvisionDefaultMembershipInTransaction(ctx, tx, accountID, role)
	return m.OrganizationID, m.ID, err
}

func (b *bloemSeededMembershipBarrier) ProvisionMembershipInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int, role string) (uuid.UUID, uuid.UUID, error) {
	if err := b.waitForPeer(ctx, tx, organizationID, accountID, role); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	m, err := b.store.ProvisionMembershipInTransaction(ctx, tx, organizationID, accountID, role)
	return m.OrganizationID, m.ID, err
}

func (b *bloemSeededMembershipBarrier) waitForPeer(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int, role string) error {
	if organizationID != b.organizationID {
		return errors.New("provisioner selected the wrong organization")
	}
	seed := bloemMembershipSeed{accountID: accountID}
	var count int
	var isolation string
	if err := tx.QueryRow(ctx, `SELECT m.id, pg_backend_pid(), current_setting('transaction_isolation'),
		(SELECT count(*) FROM organization_memberships WHERE account_id=$1)
		FROM organization_memberships m JOIN users u ON u.id=m.account_id
		WHERE m.account_id=$1 AND m.organization_id=$2 AND m.status='active' AND m.legacy_role=$3
		AND u.account_incarnation_id IS NOT NULL`, accountID, organizationID, role).
		Scan(&seed.membershipID, &seed.backendPID, &isolation, &count); err != nil {
		return fmt.Errorf("observe seeded account and membership: %w", err)
	}
	if count != 1 || isolation != "repeatable read" {
		return errors.New("barrier requires exactly one seeded membership and repeatable read")
	}
	select {
	case b.arrived <- seed:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type bloemProvisioningOutcome struct {
	index, accountID, status int
	err                      error
}

func bloemRunSeededProvisioningPair(t *testing.T, barrier *bloemSeededMembershipBarrier, run func(context.Context, int) bloemProvisioningOutcome) (map[int]bloemMembershipSeed, [2]bloemProvisioningOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	results := make(chan bloemProvisioningOutcome, 2)
	for i := range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results <- run(ctx, i)
		}()
	}
	seeds := make(map[int]bloemMembershipSeed, 2)
	var previous bloemMembershipSeed
	for range 2 {
		select {
		case seed := <-barrier.arrived:
			if seed.accountID == previous.accountID || seed.membershipID == previous.membershipID || seed.backendPID == previous.backendPID {
				t.Fatal("barrier must observe distinct accounts, memberships and database connections")
			}
			seeds[seed.accountID], previous = seed, seed
		case early := <-results:
			t.Fatalf("caller %d finished before both memberships were seeded: status=%d error=%s", early.index, early.status, bloemProvisioningError(early.err))
		case <-ctx.Done():
			t.Fatal("two seeded memberships did not reach the organization provisioning barrier")
		}
	}
	close(barrier.release)
	var outcomes [2]bloemProvisioningOutcome
	for i := range outcomes {
		select {
		case outcomes[i] = <-results:
		case <-ctx.Done():
			t.Fatal("concurrent organization provisioning did not finish")
		}
	}
	return seeds, outcomes
}

func bloemConcurrentOrganization(t *testing.T, pool *pgxpool.Pool, nonDefault bool) (*tenancy.Store, uuid.UUID, int) {
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
		return tenants, organization.ID, owner.ID
	}
	organizationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,slug,name,status,owner_account_id)
		VALUES ($1,$2,'Invited Organization','active',$3)`, organizationID, organizationID.String(), owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_groups (organization_id,name,is_default) VALUES ($1,'Default',true)`, organizationID); err != nil {
		t.Fatal(err)
	}
	return tenants, organizationID, owner.ID
}

func bloemAssertProvisionedAccount(t *testing.T, pool *pgxpool.Pool, email string, organizationID uuid.UUID, seed bloemMembershipSeed) {
	t.Helper()
	var accounts, memberships, intended int
	if err := pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM users WHERE email=$1 AND id=$2),
		(SELECT count(*) FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE u.email=$1),
		(SELECT count(*) FROM organization_memberships WHERE account_id=$2 AND organization_id=$3
		 AND id=$4 AND status='active' AND legacy_role='user')`, email, seed.accountID, organizationID, seed.membershipID).
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
