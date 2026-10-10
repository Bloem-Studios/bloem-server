package bookrequests

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateEbook is internal-only: the existing HTTP service interface accepts
// CreateInput through Create, which always creates an audiobook.
func (s *Service) CreateEbook(ctx context.Context, v Viewer, in EbookCreateInput, key string) (Request, bool, error) {
	normalized, err := normalizeInput(CreateInput{Title: in.Title, Author: in.Author, ISBN: in.ISBN, Note: in.Note})
	if err != nil {
		return Request{}, false, err
	}
	if normalized.ISBN == "" {
		return Request{}, false, fmt.Errorf("%w: ebooks require an ISBN", ErrInvalid)
	}
	return s.create(ctx, v, normalized, key, "book")
}

// GetEbook preserves access to an existing request after acquisition opt-out,
// while rechecking the caller's current account/profile and membership.
func (s *Service) GetEbook(ctx context.Context, v Viewer, id string) (Request, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Request{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	r, err := getRequest(ctx, tx, v, id, "book")
	if err != nil {
		return Request{}, err
	}
	if err = checkEbookAuthority(ctx, tx, v, false); err != nil {
		return Request{}, err
	}
	return r, nil
}

// ActEbook admits approval, denial and local cancellation only. Approval still
// awaits fulfillment; neither manual nor catalog completion is enabled here.
func (s *Service) ActEbook(ctx context.Context, v Viewer, id, action string, in ActionInput) (Request, error) {
	if action != "approve" && action != "decline" && action != "cancel" {
		return Request{}, ErrInvalid
	}
	return s.act(ctx, v, id, action, in, "book")
}

func checkEbookAuthority(ctx context.Context, q querier, v Viewer, requireOptIn bool) error {
	if v.UserID <= 0 || v.OrganizationID == uuid.Nil || !v.Admin && v.ProfileID == "" {
		return ErrForbidden
	}
	var defaultOrganization bool
	// Admin is verified authority supplied by the caller, not a hosted-status
	// signal. Even an administrator needs a current active membership; only the
	// durable organization flag decides whether this is the platform's own lane.
	err := q.QueryRow(ctx, `
 SELECT o.is_default
 FROM organizations o
 JOIN users u ON u.id=$1 AND u.enabled
 JOIN organization_memberships m ON m.organization_id=o.id AND m.account_id=u.id AND m.status='active'
 WHERE o.id=$2 AND (o.status='active' OR (o.is_default AND o.status='initializing'))
 AND ($3 OR EXISTS(SELECT 1 FROM user_profiles p WHERE p.user_id=$1 AND p.id=$4 AND p.organization_id=$2 FOR SHARE))
 FOR SHARE OF o,u,m`, v.UserID, v.OrganizationID, v.Admin, v.ProfileID).Scan(&defaultOrganization)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if !defaultOrganization {
		return ErrForbidden
	}
	if !requireOptIn {
		return nil
	}
	var value string
	err = q.QueryRow(ctx, `SELECT value FROM server_settings WHERE key=$1 FOR SHARE`, config.EbookRequestsEnabledSettingKey).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && value != "true" {
		return ErrDisabled
	}
	return err
}
