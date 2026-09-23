package policy

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ListForOrganization lists decisions from one authoritative tenant boundary.
func (r *DecisionRepository) ListForOrganization(ctx context.Context, organizationID uuid.UUID, opts ListOptions) (ListResult, error) {
	if organizationID == uuid.Nil {
		return ListResult{}, ErrDecisionNotFound
	}
	return r.list(ctx, &organizationID, opts)
}

// GetForOrganization returns a decision only when it belongs to the selected
// organization. A foreign identifier is indistinguishable from a missing one.
func (r *DecisionRepository) GetForOrganization(ctx context.Context, organizationID uuid.UUID, id int64, timestamp *time.Time) (Entry, error) {
	if organizationID == uuid.Nil {
		return Entry{}, ErrDecisionNotFound
	}
	return r.get(ctx, &organizationID, id, timestamp)
}
