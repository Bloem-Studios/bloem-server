package requests

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type transactionalUserLimitStore interface {
	UpsertUserLimitInTransaction(context.Context, pgx.Tx, UserLimit) (*UserLimit, error)
}

// UpsertUserLimitInTransaction applies the same authorization and
// normalization as UpsertUserLimit through a caller-owned transaction.
func (s *Service) UpsertUserLimitInTransaction(ctx context.Context, tx pgx.Tx, viewer Viewer, limit UserLimit) (*UserLimit, error) {
	if !viewer.IsAdmin {
		return nil, ErrForbidden
	}
	normalized, err := normalizeUserLimit(limit)
	if err != nil {
		return nil, err
	}
	if err := s.requireSameOrganization(ctx, viewer, normalized.UserID); err != nil {
		return nil, err
	}
	store, ok := s.store.(transactionalUserLimitStore)
	if !ok {
		return nil, errors.New("request limit store does not support caller-owned transactions")
	}
	return store.UpsertUserLimitInTransaction(ctx, tx, normalized)
}
