package requests

// Bloem-owned. Organization-bounded variants of the administrator queries:
// ListAdmin, which step 1 could only filter after the fact, returning short
// pages, and the queue counts.
//
// The duplicate check, title follows and the re-request cleanup stay Silo's
// and span organizations: Silo allows one active request per title
// (idx_media_requests_active_tmdb), so a profile in one organization follows a
// title another organization requested.
//
// Each request carries the organization it was filed in, in
// media_requests.organization_id (migration
// 20260923140300_bloem_media_request_organization). CreateRequest stamps it
// from the acting tenant inside its own transaction; rows inserted without a
// stamp are filled by a trigger using the requester's primary membership, the
// rule tenancy.AccountOrganization applies, so callers outside an HTTP request
// get the organization viewerOrganizationID would have picked for them.
//
// These are methods on the concrete *Repository rather than a decorator around
// Store: the service type-asserts Store for three optional capabilities
// (ConditionalStore, UserExists, transactionalUserLimitStore), and a decorator
// embedding the interface would silently drop all three.

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// organizationRequestPredicate bounds media_requests rows to one organization.
// The organization id is the caller's $1, so it must be the first bind arg.
func organizationRequestPredicate() string {
	return `organization_id = $1`
}

// stampActingOrganization records the organization a request was filed in.
//
// The acting organization is the tenant the request context was resolved
// into: an account with memberships in several organizations that files a
// request while acting in one of them files it there, not in whichever
// organization its primary membership names. Without a resolved tenant the
// trigger's fallback (the primary membership) stands. A tenant resolved for a
// different account is a wiring fault and fails the insert rather than filing
// the request somewhere arbitrary.
//
// It runs inside CreateRequest's transaction so the request is never visible
// under the fallback organization.
func stampActingOrganization(ctx context.Context, exec requestExecutor, requestID string, requester Viewer) error {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return nil
	}
	if tenant.AccountID != requester.UserID || tenant.OrganizationID == uuid.Nil {
		return fmt.Errorf("%w: request tenant does not belong to the requester", ErrForbidden)
	}
	if _, err := exec.Exec(ctx, `
		UPDATE media_requests SET organization_id = $2 WHERE id = $1
	`, requestID, tenant.OrganizationID); err != nil {
		return fmt.Errorf("stamp request organization: %w", err)
	}
	return nil
}

// RequestOrganization reports the organization a request was filed in.
func (r *Repository) RequestOrganization(ctx context.Context, requestID string) (uuid.UUID, error) {
	var organizationID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT organization_id FROM media_requests WHERE id = $1
	`, requestID).Scan(&organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("load request organization: %w", err)
	}
	return organizationID, nil
}

// ListAdminInOrganization is ListAdmin bounded to one organization. The bound
// is applied before LIMIT, so pages come back full.
func (r *Repository) ListAdminInOrganization(ctx context.Context, organizationID uuid.UUID, filter ListFilter) ([]*Request, error) {
	sqlText, args := buildRequestListSQL(organizationRequestPredicate(), []any{organizationID}, filter)
	return r.listRequests(ctx, sqlText, args)
}

// CountAdminViewsInOrganization counts only the selected organization.
func (r *Repository) CountAdminViewsInOrganization(ctx context.Context, organizationID uuid.UUID) (AdminViewCounts, error) {
	var c AdminViewCounts
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE `+adminViewSQL[AdminViewNeedsApproval]+`),
		       count(*) FILTER (WHERE `+adminViewSQL[AdminViewInProgress]+`),
		       count(*) FILTER (WHERE `+adminViewSQL[AdminViewFailed]+`),
		       count(*) FILTER (WHERE `+adminViewSQL[AdminViewDone]+`)
		FROM media_requests WHERE organization_id = $1`, organizationID).Scan(&c.NeedsApproval, &c.InProgress, &c.Failed, &c.Done)
	if err != nil {
		return AdminViewCounts{}, fmt.Errorf("count admin request views: %w", err)
	}
	return c, nil
}
