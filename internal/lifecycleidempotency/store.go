package lifecycleidempotency

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) CurrentPhase(ctx context.Context) (Phase, error) {
	if s == nil || s.pool == nil {
		return "", fmt.Errorf("lifecycle receipt store has no database pool")
	}
	var phase Phase
	if err := s.pool.QueryRow(ctx, `SELECT phase FROM public.lifecycle_idempotency_control WHERE singleton`).Scan(&phase); err != nil {
		return "", fmt.Errorf("read lifecycle idempotency phase: %w", err)
	}
	return phase, nil
}

func (s *PostgresStore) InTransaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("lifecycle receipt store has no database pool")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin lifecycle receipt transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit lifecycle receipt transaction: %w", err)
	}
	return nil
}

func (s *PostgresStore) LockHandoff(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('bloem.lifecycle_idempotency_handoff',0))`)
	if err != nil {
		return fmt.Errorf("lock lifecycle idempotency handoff: %w", err)
	}
	return nil
}

func (s *PostgresStore) Phase(ctx context.Context, tx pgx.Tx) (Phase, error) {
	var phase Phase
	if err := tx.QueryRow(ctx, `SELECT phase FROM public.lifecycle_idempotency_control WHERE singleton`).Scan(&phase); err != nil {
		return "", fmt.Errorf("read lifecycle idempotency phase: %w", err)
	}
	return phase, nil
}

func (s *PostgresStore) LockKey(ctx context.Context, tx pgx.Tx, digest Digest) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(encode($1::bytea,'hex'),0))`, digest[:])
	if err != nil {
		return fmt.Errorf("lock lifecycle idempotency key: %w", err)
	}
	return nil
}

func (s *PostgresStore) Find(ctx context.Context, tx pgx.Tx, digest Digest) (*Receipt, error) {
	var receipt Receipt
	var keyBytes, requestHash, targetDigest, actorSubject []byte
	var actorAccountID *int
	var actorIncarnation *uuid.UUID
	var targetSource *string
	var operationID *string
	var responseStatus *int
	var responseBody, responseHeaders []byte
	err := tx.QueryRow(ctx, `
SELECT idempotency_key_digest,actor_kind,actor_account_id,actor_account_incarnation_id,
       actor_subject_digest,method,route_id,request_hash,target_source,target_set_digest,
       operation_id,state,response_status,response_body,response_headers
FROM public.lifecycle_request_receipts
WHERE idempotency_key_digest=$1
FOR UPDATE`, digest[:]).Scan(
		&keyBytes, &receipt.Binding.ActorKind, &actorAccountID, &actorIncarnation,
		&actorSubject, &receipt.Binding.Method, &receipt.Binding.RouteID,
		&requestHash, &targetSource, &targetDigest, &operationID, &receipt.State,
		&responseStatus, &responseBody, &responseHeaders,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("find lifecycle receipt: %w", err)
	}
	copy(receipt.KeyDigest[:], keyBytes)
	receipt.Binding.ActorAccountID = actorAccountID
	receipt.Binding.ActorAccountIncarnationID = actorIncarnation
	copy(receipt.Binding.ActorSubjectDigest[:], actorSubject)
	copy(receipt.Binding.RequestHash[:], requestHash)
	if targetSource != nil {
		receipt.Binding.TargetSource = TargetSource(*targetSource)
	}
	copy(receipt.Binding.TargetSetDigest[:], targetDigest)
	if operationID != nil {
		receipt.Result.OperationID = *operationID
	}
	if responseStatus != nil {
		receipt.Result.Status = *responseStatus
	}
	receipt.Result.Body = append([]byte(nil), responseBody...)
	if len(responseHeaders) > 0 {
		if err := json.Unmarshal(responseHeaders, &receipt.Result.Headers); err != nil {
			return nil, fmt.Errorf("decode lifecycle receipt headers: %w", err)
		}
	}

	rows, err := tx.Query(ctx, `
SELECT organization_id,membership_id,account_id,account_incarnation_id,
       COALESCE(profile_id,''),COALESCE(resource_id,'')
FROM public.lifecycle_request_receipt_targets
WHERE idempotency_key_digest=$1
ORDER BY target_ordinal`, digest[:])
	if err != nil {
		return nil, fmt.Errorf("find lifecycle receipt targets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var target TargetBinding
		if err := rows.Scan(&target.OrganizationID, &target.MembershipID, &target.AccountID,
			&target.AccountIncarnationID, &target.ProfileID, &target.ResourceID); err != nil {
			return nil, fmt.Errorf("scan lifecycle receipt target: %w", err)
		}
		receipt.Binding.Targets = append(receipt.Binding.Targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lifecycle receipt targets: %w", err)
	}
	return &receipt, nil
}

func (s *PostgresStore) Insert(ctx context.Context, tx pgx.Tx, receipt Receipt) error {
	var actorSubject []byte
	if receipt.Binding.ActorKind == ActorPreauthIntent {
		actorSubject = receipt.Binding.ActorSubjectDigest[:]
	}
	var operationID any
	if receipt.Result.OperationID != "" {
		operationID = receipt.Result.OperationID
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO public.lifecycle_request_receipts (
    idempotency_key_digest,actor_kind,actor_account_id,actor_account_incarnation_id,
    actor_subject_digest,method,route_id,request_hash,target_source,target_set_digest,
    operation_id,state)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		receipt.KeyDigest[:], receipt.Binding.ActorKind, receipt.Binding.ActorAccountID,
		receipt.Binding.ActorAccountIncarnationID, actorSubject, receipt.Binding.Method,
		receipt.Binding.RouteID, receipt.Binding.RequestHash[:], receipt.Binding.TargetSource,
		receipt.Binding.TargetSetDigest[:], operationID, receipt.State); err != nil {
		return fmt.Errorf("insert lifecycle receipt: %w", err)
	}
	for ordinal, target := range receipt.Binding.Targets {
		var profileID, resourceID any
		if target.ProfileID != "" {
			profileID = target.ProfileID
		}
		if target.ResourceID != "" {
			resourceID = target.ResourceID
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO public.lifecycle_request_receipt_targets (
    idempotency_key_digest,target_ordinal,organization_id,membership_id,
    account_id,account_incarnation_id,profile_id,resource_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, receipt.KeyDigest[:], ordinal,
			target.OrganizationID, target.MembershipID, target.AccountID,
			target.AccountIncarnationID, profileID, resourceID); err != nil {
			return fmt.Errorf("insert lifecycle receipt target %d: %w", ordinal, err)
		}
	}
	return nil
}

func (s *PostgresStore) Complete(ctx context.Context, tx pgx.Tx, digest Digest, result Result) error {
	var operationID any
	if result.OperationID != "" {
		operationID = result.OperationID
	}
	if _, err := tx.Exec(ctx, `
UPDATE public.lifecycle_request_receipts
SET state='committed_pending',operation_id=$2
WHERE idempotency_key_digest=$1`, digest[:], operationID); err != nil {
		return fmt.Errorf("mark lifecycle receipt committed: %w", err)
	}
	headers, err := json.Marshal(result.Headers)
	if err != nil {
		return fmt.Errorf("encode lifecycle receipt headers: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE public.lifecycle_request_receipts
SET state='completed',response_status=$2,response_body=$3,response_headers=$4,
    completed_at=now()
WHERE idempotency_key_digest=$1`, digest[:], result.Status, result.Body, headers); err != nil {
		return fmt.Errorf("complete lifecycle receipt: %w", err)
	}
	return nil
}
