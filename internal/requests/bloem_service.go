package requests

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
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

// effectivePolicyForViewer resolves the requests gate against the viewer's
// profile access group. Without a validated tenant subject in the context
// only the account layer applies (no group policy).
func (s *Service) effectivePolicyForViewer(ctx context.Context, user *models.User, viewer Viewer) (access.EffectiveUserPolicy, error) {
	subject, err := access.GroupSubjectFromContext(ctx, viewer.UserID, viewer.ProfileID)
	if err != nil {
		return access.ApplyGroupPolicy(user, nil), nil //nolint:nilerr // no tenant subject: account layer only
	}
	return access.EffectivePolicyForSubject(ctx, user, subject, s.groupProvider)
}
