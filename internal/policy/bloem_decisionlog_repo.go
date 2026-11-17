package policy

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListForOrganization lists decisions from one authoritative tenant boundary.
func (r *DecisionRepository) ListForOrganization(ctx context.Context, organizationID uuid.UUID, opts ListOptions) (ListResult, error) {
	if organizationID == uuid.Nil {
		return ListResult{}, ErrDecisionNotFound
	}
	opts.organizationID = &organizationID
	return r.List(ctx, opts)
}

// GetForOrganization returns a decision only when it belongs to the selected
// organization. A foreign identifier is indistinguishable from a missing one.
// It reads what Get reads, with the organization in the WHERE clause.
func (r *DecisionRepository) GetForOrganization(ctx context.Context, organizationID uuid.UUID, id int64, timestamp *time.Time) (Entry, error) {
	if organizationID == uuid.Nil {
		return Entry{}, ErrDecisionNotFound
	}
	query := `
		SELECT id, "timestamp", organization_id, membership_id, decision_name, policy_generation, user_id,
		       COALESCE(profile_id, ''), COALESCE(session_id, ''), COALESCE(request_id, ''),
		       COALESCE(node_id, ''), allowed, eval_time_ns, input_digest,
		       input_sample, result_sample, COALESCE(error, '')
		FROM policy_decisions
		WHERE id = $1 AND organization_id = $2 AND ($3::timestamptz IS NULL OR "timestamp" = $3)
		ORDER BY "timestamp" DESC
		LIMIT 1
	`
	entry, err := scanDecisionEntry(r.pool.QueryRow(ctx, query, id, organizationID, timestamp))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, ErrDecisionNotFound
		}
		return Entry{}, err
	}
	return entry, nil
}
