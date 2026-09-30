package requests

// Bloem-owned. Organization-bounded variants of the three repository queries
// that otherwise reach across tenants:
//
//   ListAdmin           an administrator's queue, which step 1 could only
//                       filter after the fact, returning short pages
//   ListActiveByTMDB    the duplicate check, where another tenant's active
//                       request suppresses this one and is handed back to the
//                       caller
//   DeleteFailedByTMDB  the re-request cleanup, which deleted other tenants'
//                       failed rows
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

// ListActiveByTMDBInOrganization is ListActiveByTMDB bounded to one
// organization, so a tenant's duplicate check neither sees nor is suppressed by
// another tenant's active request.
func (r *Repository) ListActiveByTMDBInOrganization(ctx context.Context, organizationID uuid.UUID, mediaType MediaType, tmdbIDs []int) (map[int]*Request, error) {
	if len(tmdbIDs) == 0 {
		return map[int]*Request{}, nil
	}
	rows, err := r.pool.Query(ctx, requestSelectSQL()+`
		WHERE `+organizationRequestPredicate()+`
		  AND media_type = $2
		  AND provider = 'tmdb'
		  AND tmdb_id = ANY($3)
		  AND outcome = 'active'
		  AND status <> 'completed'
	`, organizationID, mediaType, tmdbIDs)
	if err != nil {
		return nil, fmt.Errorf("list active requests by tmdb in organization: %w", err)
	}
	defer rows.Close()

	out := map[int]*Request{}
	for rows.Next() {
		req, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out[req.TMDBID] = req
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active requests by tmdb in organization: %w", err)
	}
	return out, nil
}

// DeleteFailedByTMDBInOrganization is DeleteFailedByTMDB bounded to one
// organization, so re-requesting inside one tenant cannot delete another
// tenant's failed rows.
func (r *Repository) DeleteFailedByTMDBInOrganization(ctx context.Context, organizationID uuid.UUID, mediaType MediaType, tmdbID int) (int, error) {
	if tmdbID <= 0 {
		return 0, nil
	}
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM media_requests
		WHERE `+organizationRequestPredicate()+`
		  AND media_type = $2
		  AND provider = 'tmdb'
		  AND tmdb_id = $3
		  AND outcome = 'failed'
	`, organizationID, mediaType, tmdbID)
	if err != nil {
		return 0, fmt.Errorf("delete failed requests by tmdb in organization: %w", err)
	}
	return int(tag.RowsAffected()), nil
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

// bloemFollowTitle keeps the upstream row lock/insert atomicity within a tenant.
func (r *Repository) bloemFollowTitle(ctx context.Context, mediaType MediaType, tmdbID int, viewer Viewer) (bool, error) {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return false, nil
	}
	if tenant.AccountID != viewer.UserID || tenant.OrganizationID == uuid.Nil {
		return true, ErrForbidden
	}
	var open bool
	if err := r.pool.QueryRow(ctx, `
		WITH open_request AS (
			SELECT id FROM media_requests
			WHERE media_type = $1 AND provider = 'tmdb' AND tmdb_id = $2
			  AND outcome = 'active' AND status <> 'completed'
			  AND organization_id = $5
			LIMIT 1
			FOR SHARE
		), inserted AS (
			INSERT INTO media_request_follows (media_type, tmdb_id, user_id, profile_id, request_id)
			SELECT $1, $2, $3, $4, id FROM open_request
			ON CONFLICT (user_id, profile_id, request_id) DO NOTHING
		)
		SELECT EXISTS (SELECT 1 FROM open_request)
	`, mediaType, tmdbID, viewer.UserID, viewer.ProfileID, tenant.OrganizationID).Scan(&open); err != nil {
		return true, fmt.Errorf("follow title: %w", err)
	}
	if !open {
		return true, ErrNotRequested
	}
	return true, nil
}

// Adopt follows only from failed requests in the same organization.
func adoptOrganizationTitleFollows(ctx context.Context, exec requestExecutor, req *Request) error {
	const failed = `SELECT id FROM media_requests
		WHERE media_type = $1 AND provider = 'tmdb' AND tmdb_id = $2 AND outcome = 'failed'
		  AND organization_id = (SELECT organization_id FROM media_requests WHERE id = $3)`
	// A profile that followed two failed requests keeps its earliest follow.
	if _, err := exec.Exec(ctx, `
		DELETE FROM media_request_follows f
		USING media_request_follows keep
		WHERE f.request_id IN (`+failed+`) AND keep.request_id IN (`+failed+`)
		  AND keep.user_id = f.user_id AND keep.profile_id = f.profile_id
		  AND (keep.created_at, keep.request_id) < (f.created_at, f.request_id)
	`, req.MediaType, req.TMDBID, req.ID); err != nil {
		return fmt.Errorf("adopt title follows: %w", err)
	}
	if _, err := exec.Exec(ctx, `
		UPDATE media_request_follows SET request_id = $3
		WHERE request_id IN (`+failed+`)
	`, req.MediaType, req.TMDBID, req.ID); err != nil {
		return fmt.Errorf("adopt title follows: %w", err)
	}
	return nil
}
