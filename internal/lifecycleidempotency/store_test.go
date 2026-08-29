package lifecycleidempotency

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestReceiptFirstReplayDoesNotMutateSameNumericReplacement(t *testing.T) {
	ctx := context.Background()
	pool := newLifecycleStoreDatabase(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE lifecycle_test_effects (account_id integer PRIMARY KEY, touches integer NOT NULL)`); err != nil {
		t.Fatalf("create effect fixture: %v", err)
	}

	var accountID int
	var firstIncarnation uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO users (username,email,password_hash,role)
VALUES ('receipt-first','receipt-first@example.test','x','user')
RETURNING id,account_incarnation_id`).Scan(&accountID, &firstIncarnation); err != nil {
		t.Fatalf("create first account: %v", err)
	}
	request := Request{
		IdempotencyKey: "same-numeric-replay-key",
		Binding: Binding{
			ActorKind: ActorAuthenticatedAccount, ActorAccountID: &accountID,
			ActorAccountIncarnationID: &firstIncarnation, Method: "DELETE",
			RouteID: "admin.account.delete", RequestHash: testDigest(3),
			TargetSource: TargetPathAccount,
		},
		ResolveTargets: func(context.Context, pgx.Tx) ([]TargetBinding, error) {
			return []TargetBinding{{OrganizationID: uuid.New(), MembershipID: uuid.New(), AccountID: accountID, AccountIncarnationID: firstIncarnation}}, nil
		},
	}
	coordinator := NewCoordinator(NewPostgresStore(pool), NewHMACKeyDigester([]byte("store-test-secret-that-is-not-production")))
	mutate := func(ctx context.Context, tx pgx.Tx, _ Binding) (Result, error) {
		if _, err := tx.Exec(ctx, `INSERT INTO lifecycle_test_effects (account_id,touches) VALUES ($1,1)`, accountID); err != nil {
			return Result{}, err
		}
		return Result{Status: 200, Body: []byte(`{"incarnation":"first"}`)}, nil
	}
	first, err := coordinator.Execute(ctx, request, mutate)
	if err != nil || first.Replayed {
		t.Fatalf("first Execute() = %+v, %v", first, err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, accountID); err != nil {
		t.Fatalf("delete first account: %v", err)
	}
	var secondIncarnation uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO users (id,username,email,password_hash,role)
VALUES ($1,'receipt-second','receipt-second@example.test','x','user')
RETURNING account_incarnation_id`, accountID).Scan(&secondIncarnation); err != nil {
		t.Fatalf("create same-numeric replacement: %v", err)
	}
	if secondIncarnation == firstIncarnation {
		t.Fatal("replacement reused account incarnation")
	}
	request.ResolveTargets = func(context.Context, pgx.Tx) ([]TargetBinding, error) {
		t.Fatal("replay looked up same-numeric replacement")
		return nil, nil
	}
	replayed, err := coordinator.Execute(ctx, request, func(context.Context, pgx.Tx, Binding) (Result, error) {
		t.Fatal("replay mutated same-numeric replacement")
		return Result{}, nil
	})
	if err != nil || !replayed.Replayed || string(replayed.Body) != `{"incarnation":"first"}` {
		t.Fatalf("replay Execute() = %+v, %v", replayed, err)
	}
	var touches int
	if err := pool.QueryRow(ctx, `SELECT touches FROM lifecycle_test_effects WHERE account_id=$1`, accountID).Scan(&touches); err != nil || touches != 1 {
		t.Fatalf("effect touches = %d, %v; want 1", touches, err)
	}
}

func TestResolveAccountTargetsCapturesEveryMembershipInCanonicalOrder(t *testing.T) {
	ctx := context.Background()
	pool := newLifecycleStoreDatabase(t)

	var accountID int
	var incarnation uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO users (username,email,password_hash,role)
VALUES ('target-resolution','target-resolution@example.test','x','user')
RETURNING id,account_incarnation_id`).Scan(&accountID, &incarnation); err != nil {
		t.Fatalf("create target account: %v", err)
	}
	var defaultOrganization uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&defaultOrganization); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','user')`, defaultOrganization, accountID); err != nil {
		t.Fatalf("create default membership: %v", err)
	}
	secondOrganization := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO organizations (id,slug,name,status,owner_account_id)
VALUES ($1,$2,'Target Resolution','active',$3)`, secondOrganization, "target-resolution-"+uuid.NewString(), accountID); err != nil {
		t.Fatalf("create second organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO access_groups (organization_id,name,is_default)
VALUES ($1,$2,true)`, secondOrganization, "target-resolution-"+uuid.NewString()); err != nil {
		t.Fatalf("create second organization default group: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','user')`, secondOrganization, accountID); err != nil {
		t.Fatalf("create second membership: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin resolver transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	targets, err := ResolveAccountTargets(ctx, tx, accountID)
	if err != nil {
		t.Fatalf("ResolveAccountTargets() error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %+v, want two memberships", targets)
	}
	for index, target := range targets {
		if target.AccountID != accountID || target.AccountIncarnationID != incarnation ||
			target.OrganizationID == uuid.Nil || target.MembershipID == uuid.Nil {
			t.Fatalf("target[%d] = %+v", index, target)
		}
	}
	if targets[0].OrganizationID.String() > targets[1].OrganizationID.String() ||
		(targets[0].OrganizationID == targets[1].OrganizationID && targets[0].MembershipID.String() > targets[1].MembershipID.String()) {
		t.Fatalf("targets are not canonical: %+v", targets)
	}
}

func TestReceiptFirstCreateBindsGeneratedTargetAndReplays(t *testing.T) {
	ctx := context.Background()
	pool := newLifecycleStoreDatabase(t)
	secret := []byte("create-store-test-secret")
	coordinator := NewCoordinator(NewPostgresStore(pool), NewHMACKeyDigester(secret))
	request := Request{
		IdempotencyKey: "preauth-create-replay-key",
		Binding: Binding{
			ActorKind: ActorPreauthIntent, ActorSubjectDigest: testDigest(9), Method: "POST",
			RouteID: "auth.signup", RequestHash: testDigest(10), TargetSource: TargetBodyAccount,
		},
	}
	var organizationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&organizationID); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	var createdID int
	first, err := coordinator.ExecuteCreate(ctx, request, func(ctx context.Context, tx pgx.Tx) ([]TargetBinding, Result, error) {
		var incarnation, membershipID uuid.UUID
		if err := tx.QueryRow(ctx, `
INSERT INTO users (username,email,password_hash,role)
VALUES ($1,$2,'x','user') RETURNING id,account_incarnation_id`, "receipt-create-"+uuid.NewString(), uuid.NewString()+"@receipt-create.test").Scan(&createdID, &incarnation); err != nil {
			return nil, Result{}, err
		}
		if err := tx.QueryRow(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','user') RETURNING id`, organizationID, createdID).Scan(&membershipID); err != nil {
			return nil, Result{}, err
		}
		return []TargetBinding{{
			OrganizationID: organizationID, MembershipID: membershipID, AccountID: createdID,
			AccountIncarnationID: incarnation,
		}}, Result{Status: 201, Body: []byte(`{"created":true}`)}, nil
	})
	if err != nil || first.Replayed || first.Status != 201 {
		t.Fatalf("first ExecuteCreate() = %+v, %v", first, err)
	}
	replayed, err := coordinator.ExecuteCreate(ctx, request, func(context.Context, pgx.Tx) ([]TargetBinding, Result, error) {
		t.Fatal("replay created a second target")
		return nil, Result{}, nil
	})
	if err != nil || !replayed.Replayed || string(replayed.Body) != `{"created":true}` {
		t.Fatalf("replayed ExecuteCreate() = %+v, %v", replayed, err)
	}
	var accountCount, targetCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=$1`, createdID).Scan(&accountCount); err != nil || accountCount != 1 {
		t.Fatalf("created account count = %d, %v", accountCount, err)
	}
	keyDigest := NewHMACKeyDigester(secret)(request.IdempotencyKey)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_request_receipt_targets WHERE idempotency_key_digest=$1`, keyDigest[:]).Scan(&targetCount); err != nil || targetCount != 1 {
		t.Fatalf("receipt target count = %d, %v", targetCount, err)
	}
}

func TestResolveTenantMemberTargetCapturesReplacementIncarnation(t *testing.T) {
	ctx := context.Background()
	pool := newLifecycleStoreDatabase(t)

	tenantOrganization := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO organizations (id,slug,name,status,external_operator_id,external_service_id,slots,transcodes)
VALUES ($1,$2,'Lifecycle Tenant','initializing','operator-test',$3,2,1)`, tenantOrganization,
		"lifecycle-tenant-"+uuid.NewString(), "service-"+uuid.NewString()); err != nil {
		t.Fatalf("create tenant organization: %v", err)
	}
	create := func(username string) (int, uuid.UUID, uuid.UUID) {
		t.Helper()
		var accountID int
		var incarnation uuid.UUID
		if err := pool.QueryRow(ctx, `
INSERT INTO users (username,email,password_hash,role)
VALUES ($1,$2,'x','user')
RETURNING id,account_incarnation_id`, username, username+"@example.test").Scan(&accountID, &incarnation); err != nil {
			t.Fatalf("create account: %v", err)
		}
		var membershipID uuid.UUID
		if err := pool.QueryRow(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','user') RETURNING id`, tenantOrganization, accountID).Scan(&membershipID); err != nil {
			t.Fatalf("create membership: %v", err)
		}
		return accountID, incarnation, membershipID
	}

	accountID, firstIncarnation, firstMembership := create("tenant-member-first-" + uuid.NewString())
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin first resolver transaction: %v", err)
	}
	first, err := ResolveTenantMemberTarget(ctx, tx, tenantOrganization, accountID)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("resolve first target: %v", err)
	}
	if first != (TargetBinding{OrganizationID: tenantOrganization, MembershipID: firstMembership, AccountID: accountID, AccountIncarnationID: firstIncarnation}) {
		t.Fatalf("first target = %+v", first)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, accountID); err != nil {
		t.Fatalf("delete first account: %v", err)
	}
	var replacementIncarnation uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO users (id,username,email,password_hash,role)
VALUES ($1,$2,$3,'x','user') RETURNING account_incarnation_id`, accountID,
		"tenant-member-replacement-"+uuid.NewString(), uuid.NewString()+"@example.test").Scan(&replacementIncarnation); err != nil {
		t.Fatalf("create replacement account: %v", err)
	}
	var replacementMembership uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','user') RETURNING id`, tenantOrganization, accountID).Scan(&replacementMembership); err != nil {
		t.Fatalf("create replacement membership: %v", err)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin replacement resolver transaction: %v", err)
	}
	replacement, err := ResolveTenantMemberTarget(ctx, tx, tenantOrganization, accountID)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("resolve replacement target: %v", err)
	}
	if replacement.MembershipID != replacementMembership || replacement.AccountIncarnationID != replacementIncarnation {
		t.Fatalf("replacement target = %+v", replacement)
	}
	if replacement.MembershipID == first.MembershipID || replacement.AccountIncarnationID == first.AccountIncarnationID {
		t.Fatalf("replacement reused immutable identity: first=%+v replacement=%+v", first, replacement)
	}
}

func newLifecycleStoreDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatalf("generate database name: %v", err)
	}
	name := "lifecycle_store_" + hex.EncodeToString(random[:])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database: %v", err)
	}
	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse disposable database URL: %v", err)
	}
	testConfig.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		t.Fatalf("connect disposable database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %s: %v", name, err)
		}
		admin.Close()
	})
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database %s: %v", fmt.Sprintf("%q", name), err)
	}
	return pool
}
