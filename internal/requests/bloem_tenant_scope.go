package requests

// Bloem-owned. Silo's request service has no concept of organizations: it
// authorizes a request with `!viewer.IsAdmin && req.RequestedByUserID !=
// viewer.UserID`, which bounds ordinary users to their own requests but bounds
// administrators to nothing. On a multi-tenant deployment that lets any
// organization's administrator read and act on every other organization's
// requests.
//
// A request belongs to the organization it was filed in, which the repository
// stamps on media_requests.organization_id from the acting tenant (see
// bloem_repository_tenant.go). Request stays byte-identical to Silo: the
// organization is read through the optional requestOrganizationStore
// capability rather than carried on the struct. Stores without it (test fakes)
// fall back to the requester's primary organization.
//
// The resolver is nil-able. Left unset, every function below is a no-op and
// the service behaves exactly as Silo's does, which is what single-tenant
// deployments get.

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/tenancy"
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

// viewerOrganizationID reports the organization the viewer is acting for.
//
// A request carries the tenant its session was resolved into, and that is the
// organization the viewer acts for: an account with memberships in several
// organizations, using a session bound to one of them, must be bounded to that
// one, not to its primary membership (which prefers the default organization).
// Only a caller without a resolved tenant -- one outside an HTTP request --
// falls back to the account's primary organization. A resolved tenant for a
// different account is a wiring fault and denies.
func (s *Service) viewerOrganizationID(ctx context.Context, viewer Viewer) (uuid.UUID, error) {
	if tenant, ok := tenancy.FromContext(ctx); ok {
		if tenant.AccountID != viewer.UserID || tenant.OrganizationID == uuid.Nil {
			return uuid.Nil, fmt.Errorf("%w: request tenant does not belong to the viewer", ErrForbidden)
		}
		return tenant.OrganizationID, nil
	}
	organizationID, err := s.tenantScope.AccountOrganization(ctx, viewer.UserID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: resolving viewer organization: %w", ErrForbidden, err)
	}
	return organizationID, nil
}

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
	viewerOrg, err := s.viewerOrganizationID(ctx, viewer)
	if err != nil {
		return err
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

// requestOrganizationStore is the optional capability a store advertises when
// it records the organization each request was filed in. *Repository
// implements it.
type requestOrganizationStore interface {
	RequestOrganization(ctx context.Context, requestID string) (uuid.UUID, error)
}

// requestOrganizationID reports the organization req was filed in. A store
// that records it is authoritative; otherwise the requester's primary
// organization stands in, which is the rule rows were backfilled with.
func (s *Service) requestOrganizationID(ctx context.Context, req *Request) (uuid.UUID, error) {
	if stamped, ok := s.store.(requestOrganizationStore); ok {
		organizationID, err := stamped.RequestOrganization(ctx, req.ID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("%w: resolving request organization: %w", ErrForbidden, err)
		}
		return organizationID, nil
	}
	organizationID, err := s.tenantScope.AccountOrganization(ctx, req.RequestedByUserID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: resolving subject organization: %w", ErrForbidden, err)
	}
	return organizationID, nil
}

// requireRequestOrganization reports whether viewer may act on req: the
// organization the viewer acts for must be the one the request was filed in.
//
// ownerMayAct lets the requester reach their own request from any
// organization (reading or cancelling it), without a tenant lookup on that hot
// path. Administrative actions pass false: approving, declining or retrying is
// organization authority, so an administrator acting in one organization must
// not approve a request filed in another, even their own.
func (s *Service) requireRequestOrganization(ctx context.Context, viewer Viewer, req *Request, ownerMayAct bool) error {
	if s.tenantScope == nil || req == nil {
		return nil
	}
	if ownerMayAct && req.RequestedByUserID == viewer.UserID {
		return nil
	}
	viewerOrg, err := s.viewerOrganizationID(ctx, viewer)
	if err != nil {
		return err
	}
	requestOrg, err := s.requestOrganizationID(ctx, req)
	if err != nil {
		return err
	}
	if viewerOrg != requestOrg {
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
	viewerOrg, err := s.viewerOrganizationID(ctx, viewer)
	if err != nil {
		return nil, err
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

// tenantBoundedStore is the optional capability a store advertises when it can
// bound a query to one organization in SQL. *Repository implements it; test
// fakes generally do not, which is why every caller below degrades to the
// post-filter rather than failing.
type tenantBoundedStore interface {
	ListAdminInOrganization(ctx context.Context, organizationID uuid.UUID, filter ListFilter) ([]*Request, error)
	ListActiveByTMDBInOrganization(ctx context.Context, organizationID uuid.UUID, mediaType MediaType, tmdbIDs []int) (map[int]*Request, error)
	DeleteFailedByTMDBInOrganization(ctx context.Context, organizationID uuid.UUID, mediaType MediaType, tmdbID int) (int, error)
}

// viewerOrganization reports the organization to bound a query to, and whether
// bounding applies at all. It applies only when a resolver is wired and the
// store can act on it.
func (s *Service) viewerOrganization(ctx context.Context, viewer Viewer) (uuid.UUID, tenantBoundedStore, bool, error) {
	if s.tenantScope == nil {
		return uuid.Nil, nil, false, nil
	}
	bounded, ok := s.store.(tenantBoundedStore)
	if !ok {
		return uuid.Nil, nil, false, nil
	}
	organizationID, err := s.viewerOrganizationID(ctx, viewer)
	if err != nil {
		return uuid.Nil, nil, false, err
	}
	return organizationID, bounded, true, nil
}

// listAdminBounded serves an administrator's queue.
//
// With a bounding store the organization is applied inside the statement, so
// LIMIT counts only rows the viewer may see and pages come back full. Without
// one it falls back to step 1's post-filter, which is still safe but can return
// a short page.
func (s *Service) listAdminBounded(ctx context.Context, viewer Viewer, filter ListFilter) ([]*Request, error) {
	organizationID, bounded, applies, err := s.viewerOrganization(ctx, viewer)
	if err != nil {
		return nil, err
	}
	if applies {
		return bounded.ListAdminInOrganization(ctx, organizationID, filter)
	}
	reqs, err := s.store.ListAdmin(ctx, filter)
	if err != nil {
		return nil, err
	}
	return s.boundToViewerOrganization(ctx, viewer, reqs)
}

// activeByTMDBForViewer runs the duplicate check within the viewer's
// organization, so one tenant's active request neither suppresses another
// tenant's nor is handed back to it.
func (s *Service) activeByTMDBForViewer(ctx context.Context, viewer Viewer, mediaType MediaType, tmdbIDs []int) (map[int]*Request, error) {
	organizationID, bounded, applies, err := s.viewerOrganization(ctx, viewer)
	if err != nil {
		return nil, err
	}
	if applies {
		return bounded.ListActiveByTMDBInOrganization(ctx, organizationID, mediaType, tmdbIDs)
	}
	return s.store.ListActiveByTMDB(ctx, mediaType, tmdbIDs)
}

// deleteFailedByTMDBForViewer clears prior failed rows only inside the viewer's
// organization.
func (s *Service) deleteFailedByTMDBForViewer(ctx context.Context, viewer Viewer, mediaType MediaType, tmdbID int) (int, error) {
	organizationID, bounded, applies, err := s.viewerOrganization(ctx, viewer)
	if err != nil {
		return 0, err
	}
	if applies {
		return bounded.DeleteFailedByTMDBInOrganization(ctx, organizationID, mediaType, tmdbID)
	}
	return s.store.DeleteFailedByTMDB(ctx, mediaType, tmdbID)
}

// request_settings and request_integrations carry no organization column:
// there is one row of request settings and one set of Radarr/Sonarr
// integrations for the whole server. Silo gates both on IsAdmin alone, so on a
// multi-tenant deployment any tenant's administrator could read the operator's
// integration base URLs and repoint the server's download clients.
//
// The operator is the default organization -- the one organizations.is_default
// marks, which tenancy provisions before any tenant exists. Administrators
// there keep server-wide authority; administrators of a provisioned tenant do
// not. Single-tenant deployments are unaffected: every account belongs to the
// default organization, so the check always passes.

// defaultOrganizationResolver is the optional half of TenantScopeResolver that
// names the operator's organization. *tenancy.Store satisfies it.
type defaultOrganizationResolver interface {
	DefaultOrganization(ctx context.Context) (tenancy.Organization, error)
}

// requirePlatformAuthority denies administrators outside the operator's own
// organization. Like the rest of this file it is a no-op when no resolver is
// wired, and it fails closed once one is -- including a resolver that cannot
// name the default organization, since then no administrator can be shown to
// belong to it.
func (s *Service) requirePlatformAuthority(ctx context.Context, viewer Viewer) error {
	if s.tenantScope == nil {
		return nil
	}
	defaults, ok := s.tenantScope.(defaultOrganizationResolver)
	if !ok {
		return fmt.Errorf("%w: tenant scope resolver cannot name the operator organization", ErrForbidden)
	}
	viewerOrg, err := s.viewerOrganizationID(ctx, viewer)
	if err != nil {
		return err
	}
	operator, err := defaults.DefaultOrganization(ctx)
	if err != nil {
		return fmt.Errorf("%w: resolving the operator organization: %w", ErrForbidden, err)
	}
	if viewerOrg != operator.ID {
		return ErrForbidden
	}
	return nil
}
