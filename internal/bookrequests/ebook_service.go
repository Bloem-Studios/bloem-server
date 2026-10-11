package bookrequests

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) CapabilityEbook(ctx context.Context, v Viewer) (Capability, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Capability{}, err
	}
	defer tx.Rollback(ctx)
	if err = checkEbookAuthority(ctx, tx, v, false); err != nil {
		return Capability{}, err
	}
	p, err := readPolicyFor(ctx, tx, v, "book")
	if err != nil {
		return Capability{}, err
	}
	p.ManualFulfilment = false
	if err = checkEbookAuthority(ctx, tx, v, true); errors.Is(err, ErrDisabled) {
		p.Enabled, p.CanRequest = false, false
	} else if err != nil {
		return Capability{}, err
	}
	if s.ebookFulfillment != nil {
		in, loadErr := s.selectEbookIntegration(ctx, tx, v.OrganizationID)
		if loadErr == nil && in.LibraryID > 0 {
			p.FulfilmentBackend = "bookwarehouse"
		} else if loadErr != nil && !errors.Is(loadErr, ErrConflict) {
			return Capability{}, loadErr
		}
	}
	return p.Capability, nil
}

// ListEbooks keeps authorized history visible after acquisition is disabled.
// The admin queue is organization-scoped; ordinary viewers see their profile.
func (s *Service) ListEbooks(ctx context.Context, v Viewer, limit, offset int) ([]Request, error) {
	if offset < 0 {
		return nil, ErrInvalid
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = checkEbookAuthority(ctx, tx, v, false); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+selectColumns+` FROM bloem_book_requests WHERE organization_id=$1 AND media_type='book' AND ($2 OR (requested_by_user_id=$3 AND requested_by_profile_id=$4)) ORDER BY created_at DESC,id DESC LIMIT $5 OFFSET $6`, v.OrganizationID, v.Admin, v.UserID, v.ProfileID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateEbook admits the separate ISBN ebook lane. Create and CreateInput
// retain their audiobook behavior and payload-digest semantics.
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

// ActEbook admits approval, denial, local cancellation and admin retries.
// Retrying checks the retained submission; completion belongs to reconciliation.
func (s *Service) ActEbook(ctx context.Context, v Viewer, id, action string, in ActionInput) (Request, error) {
	if action != "approve" && action != "decline" && action != "cancel" && action != "retry" {
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
