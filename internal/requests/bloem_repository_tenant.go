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
// media_requests has no organization column. A request belongs to the
// organization of the account that made it, so each query joins through
// tenancy.PrimaryMembershipSQL -- the same selection rule
// tenancy.AccountOrganization applies one account at a time.
//
// These are methods on the concrete *Repository rather than a decorator around
// Store: the service type-asserts Store for three optional capabilities
// (ConditionalStore, UserExists, transactionalUserLimitStore), and a decorator
// embedding the interface would silently drop all three.

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// organizationRequestPredicate bounds media_requests rows to one organization.
// The organization id is the caller's $1, so it must be the first bind arg.
func organizationRequestPredicate() string {
	return `requested_by_user_id IN (
		SELECT primary_membership.account_id
		FROM (` + tenancy.PrimaryMembershipSQL + `
		) AS primary_membership
		WHERE primary_membership.organization_id = $1
	)`
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
