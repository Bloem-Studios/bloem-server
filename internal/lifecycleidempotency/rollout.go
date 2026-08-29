package lifecycleidempotency

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrClientEvidenceIncomplete = errors.New("lifecycle idempotency client evidence is incomplete")
	ErrRolloutDigestMismatch    = errors.New("lifecycle idempotency rollout digest mismatch")
)

var commitSHAFormat = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

type ClientEvidence struct {
	Client               string
	CommitSHA            string
	SuiteDigest          Digest
	ReleasedAt           time.Time
	ReleaseChannelDigest Digest
	RecordedAt           time.Time
}

type RolloutStatus struct {
	Phase                 Phase
	FinalizedAt           *time.Time
	FinalizedRouteDigest  Digest
	FinalizedSchemaDigest Digest
	Evidence              []ClientEvidence
}

type FinalizeInput struct {
	ObservedRouteDigest  Digest
	ExpectedRouteDigest  Digest
	ObservedSchemaDigest Digest
	ExpectedSchemaDigest Digest
	ProductionWebDigest  Digest
}

type Rollout struct {
	pool *pgxpool.Pool
}

func NewRollout(pool *pgxpool.Pool) *Rollout { return &Rollout{pool: pool} }

func (r *Rollout) RecordClientEvidence(ctx context.Context, evidence ClientEvidence) error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("lifecycle rollout has no database pool")
	}
	if !validEvidence(evidence) {
		return fmt.Errorf("invalid lifecycle client evidence")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin lifecycle evidence transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.lifecycle_idempotency_evidence_writer','v1',true)`); err != nil {
		return fmt.Errorf("authorize lifecycle evidence writer: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO public.lifecycle_idempotency_client_release_evidence (
    client,commit_sha,suite_digest,released_at,release_channel_digest)
VALUES ($1,$2,$3,$4,$5)`, evidence.Client, evidence.CommitSHA,
		evidence.SuiteDigest[:], evidence.ReleasedAt, evidence.ReleaseChannelDigest[:]); err != nil {
		return fmt.Errorf("record lifecycle client evidence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit lifecycle client evidence: %w", err)
	}
	return nil
}

func (r *Rollout) Status(ctx context.Context) (RolloutStatus, error) {
	if r == nil || r.pool == nil {
		return RolloutStatus{}, fmt.Errorf("lifecycle rollout has no database pool")
	}
	var status RolloutStatus
	var routeDigest, schemaDigest []byte
	if err := r.pool.QueryRow(ctx, `
SELECT phase,finalized_at,finalized_route_digest,finalized_schema_digest
FROM public.lifecycle_idempotency_control WHERE singleton`).Scan(
		&status.Phase, &status.FinalizedAt, &routeDigest, &schemaDigest); err != nil {
		return RolloutStatus{}, fmt.Errorf("read lifecycle rollout status: %w", err)
	}
	copy(status.FinalizedRouteDigest[:], routeDigest)
	copy(status.FinalizedSchemaDigest[:], schemaDigest)
	rows, err := r.pool.Query(ctx, `
SELECT client,commit_sha,suite_digest,released_at,release_channel_digest,recorded_at
FROM public.lifecycle_idempotency_client_release_evidence ORDER BY client`)
	if err != nil {
		return RolloutStatus{}, fmt.Errorf("read lifecycle client evidence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var evidence ClientEvidence
		var suiteDigest, channelDigest []byte
		if err := rows.Scan(&evidence.Client, &evidence.CommitSHA, &suiteDigest,
			&evidence.ReleasedAt, &channelDigest, &evidence.RecordedAt); err != nil {
			return RolloutStatus{}, fmt.Errorf("scan lifecycle client evidence: %w", err)
		}
		copy(evidence.SuiteDigest[:], suiteDigest)
		copy(evidence.ReleaseChannelDigest[:], channelDigest)
		status.Evidence = append(status.Evidence, evidence)
	}
	if err := rows.Err(); err != nil {
		return RolloutStatus{}, fmt.Errorf("iterate lifecycle client evidence: %w", err)
	}
	return status, nil
}

func (r *Rollout) Finalize(ctx context.Context, input FinalizeInput) error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("lifecycle rollout has no database pool")
	}
	if input.ObservedRouteDigest == (Digest{}) || input.ObservedSchemaDigest == (Digest{}) ||
		input.ProductionWebDigest == (Digest{}) ||
		input.ObservedRouteDigest != input.ExpectedRouteDigest ||
		input.ObservedSchemaDigest != input.ExpectedSchemaDigest {
		return ErrRolloutDigestMismatch
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin lifecycle finalizer: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('bloem.lifecycle_idempotency_handoff',0))`); err != nil {
		return fmt.Errorf("lock lifecycle finalizer handoff: %w", err)
	}
	var phase Phase
	var storedRoute, storedSchema []byte
	if err := tx.QueryRow(ctx, `
SELECT phase,finalized_route_digest,finalized_schema_digest
FROM public.lifecycle_idempotency_control WHERE singleton FOR UPDATE`).Scan(
		&phase, &storedRoute, &storedSchema); err != nil {
		return fmt.Errorf("lock lifecycle rollout control: %w", err)
	}
	if phase == PhaseRequired {
		var routeDigest, schemaDigest Digest
		copy(routeDigest[:], storedRoute)
		copy(schemaDigest[:], storedSchema)
		if routeDigest != input.ExpectedRouteDigest || schemaDigest != input.ExpectedSchemaDigest {
			return ErrRolloutDigestMismatch
		}
		return tx.Commit(ctx)
	}

	evidence, err := loadEvidence(ctx, tx)
	if err != nil {
		return err
	}
	if len(evidence) != 3 || evidence["web"].Client == "" || evidence["apple"].Client == "" || evidence["android"].Client == "" {
		return ErrClientEvidenceIncomplete
	}
	if evidence["web"].ReleaseChannelDigest != input.ProductionWebDigest {
		return ErrRolloutDigestMismatch
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.lifecycle_idempotency_finalizer','v1',true)`); err != nil {
		return fmt.Errorf("authorize lifecycle finalizer: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE public.lifecycle_idempotency_control
SET phase='required',finalized_at=now(),finalized_route_digest=$1,finalized_schema_digest=$2
WHERE singleton AND phase='optional'`, input.ExpectedRouteDigest[:], input.ExpectedSchemaDigest[:]); err != nil {
		return fmt.Errorf("finalize lifecycle idempotency: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit lifecycle finalizer: %w", err)
	}
	return nil
}

func loadEvidence(ctx context.Context, tx pgx.Tx) (map[string]ClientEvidence, error) {
	rows, err := tx.Query(ctx, `
SELECT client,commit_sha,suite_digest,released_at,release_channel_digest,recorded_at
FROM public.lifecycle_idempotency_client_release_evidence FOR SHARE`)
	if err != nil {
		return nil, fmt.Errorf("lock lifecycle client evidence: %w", err)
	}
	defer rows.Close()
	evidenceByClient := make(map[string]ClientEvidence, 3)
	for rows.Next() {
		var evidence ClientEvidence
		var suiteDigest, channelDigest []byte
		if err := rows.Scan(&evidence.Client, &evidence.CommitSHA, &suiteDigest,
			&evidence.ReleasedAt, &channelDigest, &evidence.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan locked lifecycle client evidence: %w", err)
		}
		copy(evidence.SuiteDigest[:], suiteDigest)
		copy(evidence.ReleaseChannelDigest[:], channelDigest)
		evidenceByClient[evidence.Client] = evidence
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate locked lifecycle client evidence: %w", err)
	}
	return evidenceByClient, nil
}

func validEvidence(evidence ClientEvidence) bool {
	return (evidence.Client == "web" || evidence.Client == "apple" || evidence.Client == "android") &&
		commitSHAFormat.MatchString(evidence.CommitSHA) && evidence.SuiteDigest != (Digest{}) &&
		evidence.ReleaseChannelDigest != (Digest{}) && !evidence.ReleasedAt.IsZero()
}
