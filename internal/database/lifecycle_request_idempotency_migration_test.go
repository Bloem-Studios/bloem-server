package database

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/migrations"
)

const lifecycleRequestIdempotencyPreviousMigration int64 = 20260829085838

func TestAccountIncarnationMigrationBackfillsAndOwnsIdentity(t *testing.T) {
	ctx := context.Background()
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate latest: %v", err)
	}
	if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", lifecycleRequestIdempotencyPreviousMigration); err != nil {
		t.Fatalf("prepare predecessor schema: %v", err)
	}

	var legacyID int
	if err := pool.QueryRow(ctx, `
INSERT INTO public.users (username,email,password_hash,role)
VALUES ('incarnation-legacy','incarnation-legacy@example.test','x','user')
RETURNING id`).Scan(&legacyID); err != nil {
		t.Fatalf("seed legacy account: %v", err)
	}
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("apply lifecycle idempotency migration: %v", err)
	}

	var legacyIncarnation uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT account_incarnation_id FROM public.users WHERE id=$1`, legacyID).Scan(&legacyIncarnation); err != nil {
		t.Fatalf("read backfilled incarnation: %v", err)
	}
	if legacyIncarnation == uuid.Nil {
		t.Fatal("legacy account received nil incarnation")
	}

	callerSelected := uuid.New()
	var generated uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO public.users (username,email,password_hash,role,account_incarnation_id)
VALUES ('incarnation-new','incarnation-new@example.test','x','user',$1)
RETURNING account_incarnation_id`, callerSelected).Scan(&generated); err != nil {
		t.Fatalf("create account with caller-supplied incarnation: %v", err)
	}
	if generated == uuid.Nil || generated == callerSelected {
		t.Fatalf("database incarnation = %s, must be generated independently of caller value %s", generated, callerSelected)
	}

	if _, err := pool.Exec(ctx, `UPDATE public.users SET account_incarnation_id=$1 WHERE id=$2`, uuid.New(), legacyID); err == nil || !strings.Contains(err.Error(), "account_incarnation_immutable") {
		t.Fatalf("replace immutable incarnation error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.users SET account_incarnation_id=NULL WHERE id=$1`, legacyID); err == nil {
		t.Fatal("nulling immutable incarnation succeeded")
	}
}

func TestLifecycleIdempotencyMigrationCreatesOptionalRetainedReceiptSchema(t *testing.T) {
	ctx := context.Background()
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate latest: %v", err)
	}

	var phase string
	if err := pool.QueryRow(ctx, `SELECT phase FROM public.lifecycle_idempotency_control WHERE singleton`).Scan(&phase); err != nil {
		t.Fatalf("read lifecycle idempotency phase: %v", err)
	}
	if phase != "optional" {
		t.Fatalf("phase = %q, want optional", phase)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.lifecycle_idempotency_control SET phase='required', finalized_at=now() WHERE singleton`); err == nil {
		t.Fatal("unguarded required transition succeeded")
	}

	var accountID int
	var incarnation uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO public.users (username,email,password_hash,role)
VALUES ('receipt-actor','receipt-actor@example.test','x','user')
RETURNING id,account_incarnation_id`).Scan(&accountID, &incarnation); err != nil {
		t.Fatalf("create receipt actor: %v", err)
	}
	keyDigest := make([]byte, 32)
	requestDigest := make([]byte, 32)
	targetDigest := make([]byte, 32)
	keyDigest[0], requestDigest[0], targetDigest[0] = 1, 2, 3
	if _, err := pool.Exec(ctx, `
INSERT INTO public.lifecycle_request_receipts (
    idempotency_key_digest,actor_kind,actor_account_id,
    actor_account_incarnation_id,method,route_id,request_hash,
    target_source,target_set_digest,state)
VALUES ($1,'authenticated_account',$2,$3,'DELETE','admin.account.delete',$4,
        'path_account',$5,'bound')`, keyDigest, accountID, incarnation, requestDigest, targetDigest); err != nil {
		t.Fatalf("insert retained receipt: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM public.users WHERE id=$1`, accountID); err != nil {
		t.Fatalf("delete original account: %v", err)
	}
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.lifecycle_request_receipts WHERE idempotency_key_digest=$1`, keyDigest).Scan(&receipts); err != nil {
		t.Fatalf("read retained receipt: %v", err)
	}
	if receipts != 1 {
		t.Fatalf("retained receipts = %d, want 1", receipts)
	}
}

func TestLifecycleIdempotencyMigrationFreezesCompletedReceiptAndTargets(t *testing.T) {
	ctx := context.Background()
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate latest: %v", err)
	}
	var accountID int
	var incarnation uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO users (username,email,password_hash,role)
VALUES ('receipt-freeze','receipt-freeze@example.test','x','user')
RETURNING id,account_incarnation_id`).Scan(&accountID, &incarnation); err != nil {
		t.Fatalf("create account: %v", err)
	}
	keyDigest, requestDigest, targetDigest := make([]byte, 32), make([]byte, 32), make([]byte, 32)
	keyDigest[0], requestDigest[0], targetDigest[0] = 11, 12, 13
	organizationID, membershipID := uuid.New(), uuid.New()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin receipt fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO lifecycle_request_receipts (
    idempotency_key_digest,actor_kind,actor_account_id,actor_account_incarnation_id,
    method,route_id,request_hash,target_source,target_set_digest,state)
VALUES ($1,'authenticated_account',$2,$3,'DELETE','account.delete',$4,'path_account',$5,'bound')`, keyDigest, accountID, incarnation, requestDigest, targetDigest); err != nil {
		t.Fatalf("insert receipt fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO lifecycle_request_receipt_targets (
    idempotency_key_digest,target_ordinal,organization_id,membership_id,account_id,account_incarnation_id)
VALUES ($1,0,$2,$3,$4,$5)`, keyDigest, organizationID, membershipID, accountID, incarnation); err != nil {
		t.Fatalf("insert receipt target fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE lifecycle_request_receipts SET state='committed_pending' WHERE idempotency_key_digest=$1`, keyDigest); err != nil {
		t.Fatalf("mark receipt pending fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE lifecycle_request_receipts
SET state='completed',response_status=204,response_body='first',response_headers='{}',completed_at=now()
WHERE idempotency_key_digest=$1`, keyDigest); err != nil {
		t.Fatalf("complete receipt fixture update: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit receipt fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lifecycle_request_receipts SET response_body='changed' WHERE idempotency_key_digest=$1`, keyDigest); err == nil {
		t.Fatal("completed receipt response changed")
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO lifecycle_request_receipt_targets (
    idempotency_key_digest,target_ordinal,organization_id,membership_id,account_id,account_incarnation_id)
VALUES ($1,1,$2,$3,$4,$5)`, keyDigest, uuid.New(), uuid.New(), accountID, incarnation); err == nil {
		t.Fatal("target appended after receipt completion")
	}
}
