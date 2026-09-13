package requests

// Bloem-owned. Silo's request service has no concept of organizations: it
// authorizes a request with `!viewer.IsAdmin && req.RequestedByUserID !=
// viewer.UserID`, which bounds ordinary users to their own requests but bounds
// administrators to nothing. On a multi-tenant deployment that lets any
// organization's administrator read and act on every other organization's
// requests.
//
// A request's organization is derivable rather than stored: it is the
// organization of the account that made it. That is why nothing here needs a
// migration, a column on media_requests, or a field on Request — types.go and
// store.go stay byte-identical to Silo.
//
// The resolver is nil-able. Left unset, every function below is a no-op and
// the service behaves exactly as Silo's does, which is what single-tenant
// deployments get.

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// TenantScopeResolver maps an account to the organization that owns it.
// *tenancy.Store satisfies this as it stands; nothing in tenancy changes.
type TenantScopeResolver interface {
	AccountOrganization(ctx context.Context, accountID int) (uuid.UUID, error)
}

// SetTenantScopeResolver bounds administrator authority to the viewer's own
// organization. Unset, administrators keep Silo's server-wide authority.
func (s *Service) SetTenantScopeResolver(r TenantScopeResolver) { s.tenantScope = r }

// requireSameOrganization reports whether viewer may act on something owned by
// subjectUserID.
//
// It fails closed: a resolver that is wired but cannot answer denies the
// action rather than falling through to Silo's unbounded administrator check.
// The cause travels in the message while the sentinel stays ErrForbidden, so
// callers keep returning 403 and the reason is still greppable in logs.
func (s *Service) requireSameOrganization(ctx context.Context, viewer Viewer, subjectUserID int) error {
	if s.tenantScope == nil {
		return nil
	}
	// Acting on your own resource needs no organization comparison, and this
	// keeps the common self-service path free of two database round trips.
	if subjectUserID == viewer.UserID {
		return nil
	}
	viewerOrg, err := s.tenantScope.AccountOrganization(ctx, viewer.UserID)
	if err != nil {
		return fmt.Errorf("%w: resolving viewer organization: %w", ErrForbidden, err)
	}
	subjectOrg, err := s.tenantScope.AccountOrganization(ctx, subjectUserID)
	if err != nil {
		return fmt.Errorf("%w: resolving subject organization: %w", ErrForbidden, err)
	}
	if viewerOrg != subjectOrg {
		return ErrForbidden
	}
	return nil
}

// boundToViewerOrganization drops requests belonging to other organizations.
//
// Interim measure. Filtering after the query can return a short page, because
// the limit was applied before the bound. Step 2 moves this into a Bloem-owned
// Store decorator that bounds in SQL; until then a short page is the price of
// not leaking, which is the right way round.
func (s *Service) boundToViewerOrganization(ctx context.Context, viewer Viewer, reqs []*Request) ([]*Request, error) {
	if s.tenantScope == nil || len(reqs) == 0 {
		return reqs, nil
	}
	viewerOrg, err := s.tenantScope.AccountOrganization(ctx, viewer.UserID)
	if err != nil {
		return nil, fmt.Errorf("%w: resolving viewer organization: %w", ErrForbidden, err)
	}
	// One lookup per distinct requester, not per row: an admin queue is
	// typically many requests across few accounts.
	orgOf := make(map[int]uuid.UUID, len(reqs))
	bounded := make([]*Request, 0, len(reqs))
	for _, req := range reqs {
		if req == nil {
			continue
		}
		org, seen := orgOf[req.RequestedByUserID]
		if !seen {
			if org, err = s.tenantScope.AccountOrganization(ctx, req.RequestedByUserID); err != nil {
				// One unresolvable requester must not fail the whole queue;
				// drop the row instead, which is the fail-closed choice.
				orgOf[req.RequestedByUserID] = uuid.Nil
				continue
			}
			orgOf[req.RequestedByUserID] = org
		}
		if org == uuid.Nil {
			continue
		}
		if org == viewerOrg {
			bounded = append(bounded, req)
		}
	}
	return bounded, nil
}
