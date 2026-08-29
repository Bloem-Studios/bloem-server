package lifecycleidempotency

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"sort"
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
	Phase                  Phase
	FinalizedAt            *time.Time
	FinalizedRouteDigest   Digest
	FinalizedSchemaDigest  Digest
	CurrentRouteDigest     Digest
	CurrentSchemaDigest    Digest
	RouteMatchesFinalized  bool
	SchemaMatchesFinalized bool
	Evidence               []ClientEvidence
}

type FinalizeInput struct {
	ExpectedRouteDigest  Digest
	ExpectedSchemaDigest Digest
	ProductionWebDigest  Digest
}

type Rollout struct {
	pool        *pgxpool.Pool
	routeDigest Digest
}

func NewRollout(pool *pgxpool.Pool, routeDigest Digest) *Rollout {
	return &Rollout{pool: pool, routeDigest: routeDigest}
}

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
	status.CurrentRouteDigest = r.routeDigest
	currentSchema, err := currentSchemaDigest(ctx, r.pool)
	if err != nil {
		return RolloutStatus{}, err
	}
	status.CurrentSchemaDigest = currentSchema
	if status.Phase == PhaseRequired {
		status.RouteMatchesFinalized = status.CurrentRouteDigest == status.FinalizedRouteDigest
		status.SchemaMatchesFinalized = status.CurrentSchemaDigest == status.FinalizedSchemaDigest
	}
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
	if r.routeDigest == (Digest{}) || input.ExpectedRouteDigest == (Digest{}) ||
		input.ExpectedSchemaDigest == (Digest{}) || input.ProductionWebDigest == (Digest{}) ||
		r.routeDigest != input.ExpectedRouteDigest {
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
	currentSchema, err := currentSchemaDigest(ctx, tx)
	if err != nil {
		return err
	}
	if currentSchema != input.ExpectedSchemaDigest {
		return ErrRolloutDigestMismatch
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

type schemaManifestEntry struct {
	kind, identity, definition string
}

type schemaManifestQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func currentSchemaDigest(ctx context.Context, queryer schemaManifestQuerier) (Digest, error) {
	rows, err := queryer.Query(ctx, `
WITH lifecycle_relations AS (
    SELECT class.oid, class.relname
    FROM pg_class AS class
    JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
    WHERE namespace.nspname = 'public'
      AND class.relkind IN ('r','p')
      AND class.relname IN (
        'lifecycle_idempotency_control',
        'lifecycle_idempotency_client_release_evidence',
        'lifecycle_request_receipts',
        'lifecycle_request_receipt_targets'
      )
), manifest AS (
    SELECT 'column'::text AS kind,
           relation.relname || '.' || attribute.attname AS identity,
           concat_ws('|', attribute.attnum::text, format_type(attribute.atttypid, attribute.atttypmod),
               attribute.attnotnull::text, COALESCE(pg_get_expr(default_value.adbin, default_value.adrelid), '')) AS definition
    FROM lifecycle_relations AS relation
    JOIN pg_attribute AS attribute ON attribute.attrelid = relation.oid
    LEFT JOIN pg_attrdef AS default_value
      ON default_value.adrelid = attribute.attrelid AND default_value.adnum = attribute.attnum
    WHERE attribute.attnum > 0 AND NOT attribute.attisdropped
    UNION ALL
    SELECT 'column', 'users.' || attribute.attname,
           concat_ws('|', attribute.attnum::text, format_type(attribute.atttypid, attribute.atttypmod),
               attribute.attnotnull::text, COALESCE(pg_get_expr(default_value.adbin, default_value.adrelid), ''))
    FROM pg_attribute AS attribute
    JOIN pg_class AS class ON class.oid = attribute.attrelid
    JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
    LEFT JOIN pg_attrdef AS default_value
      ON default_value.adrelid = attribute.attrelid AND default_value.adnum = attribute.attnum
    WHERE namespace.nspname = 'public' AND class.relname = 'users'
      AND attribute.attname = 'account_incarnation_id' AND NOT attribute.attisdropped
    UNION ALL
    SELECT 'constraint', relation.relname || '.' || constraint_row.conname,
           pg_get_constraintdef(constraint_row.oid, false)
    FROM lifecycle_relations AS relation
    JOIN pg_constraint AS constraint_row ON constraint_row.conrelid = relation.oid
    UNION ALL
    SELECT 'constraint', 'users.' || constraint_row.conname, pg_get_constraintdef(constraint_row.oid, false)
    FROM pg_constraint AS constraint_row
    JOIN pg_class AS class ON class.oid = constraint_row.conrelid
    JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
    WHERE namespace.nspname = 'public' AND class.relname = 'users'
      AND constraint_row.conname = 'users_account_incarnation_id_key'
    UNION ALL
    SELECT 'index', indexes.tablename || '.' || indexes.indexname, indexes.indexdef
    FROM pg_indexes AS indexes
    WHERE indexes.schemaname = 'public'
      AND indexes.tablename IN (SELECT relname FROM lifecycle_relations)
    UNION ALL
    SELECT 'trigger', class.relname || '.' || trigger.tgname, pg_get_triggerdef(trigger.oid, false)
    FROM pg_trigger AS trigger
    JOIN pg_class AS class ON class.oid = trigger.tgrelid
    JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
    WHERE namespace.nspname = 'public' AND NOT trigger.tgisinternal
      AND (class.relname IN (SELECT relname FROM lifecycle_relations)
           OR (class.relname = 'users' AND trigger.tgname = 'users_account_incarnation_guard'))
    UNION ALL
    SELECT 'function', procedure.proname || '(' || pg_get_function_identity_arguments(procedure.oid) || ')',
           pg_get_functiondef(procedure.oid)
    FROM pg_proc AS procedure
    JOIN pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
    WHERE namespace.nspname = 'public'
      AND procedure.proname IN (
        'guard_account_incarnation',
        'guard_lifecycle_idempotency_control',
        'guard_lifecycle_idempotency_client_evidence',
        'guard_lifecycle_request_receipt',
        'guard_lifecycle_request_receipt_target',
        'reject_unresolved_lifecycle_request_receipt'
      )
)
SELECT kind, identity, definition FROM manifest ORDER BY kind, identity, definition`)
	if err != nil {
		return Digest{}, fmt.Errorf("read lifecycle schema manifest: %w", err)
	}
	defer rows.Close()
	entries := make([]schemaManifestEntry, 0, 64)
	for rows.Next() {
		var entry schemaManifestEntry
		if err := rows.Scan(&entry.kind, &entry.identity, &entry.definition); err != nil {
			return Digest{}, fmt.Errorf("scan lifecycle schema manifest: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return Digest{}, fmt.Errorf("iterate lifecycle schema manifest: %w", err)
	}
	if len(entries) == 0 {
		return Digest{}, errors.New("lifecycle schema manifest is empty")
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].kind != entries[j].kind {
			return entries[i].kind < entries[j].kind
		}
		if entries[i].identity != entries[j].identity {
			return entries[i].identity < entries[j].identity
		}
		return entries[i].definition < entries[j].definition
	})
	hash := sha256.New()
	var length [8]byte
	for _, entry := range entries {
		for _, part := range []string{entry.kind, entry.identity, entry.definition} {
			binary.BigEndian.PutUint64(length[:], uint64(len(part)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(part))
		}
	}
	var digest Digest
	copy(digest[:], hash.Sum(nil))
	return digest, nil
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
