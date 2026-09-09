package organizations

import (
	"context"
	"fmt"
	"slices"

	"github.com/Silo-Server/silo-server/internal/access"
)

type BoundarySource interface {
	ResolveViewerBoundary(context.Context, int64) (ViewerBoundary, error)
}

type ScopeResolver interface {
	Resolve(context.Context, access.ResolveInput) (access.Scope, error)
}

// ViewerResolver adds an organization ceiling without changing Silo's policy engine.
type ViewerResolver struct {
	users      access.UserRepository
	boundaries BoundarySource
	inner      ScopeResolver
}

func NewViewerResolver(users access.UserRepository, boundaries BoundarySource, inner ScopeResolver) *ViewerResolver {
	return &ViewerResolver{users: users, boundaries: boundaries, inner: inner}
}

func (r *ViewerResolver) Resolve(ctx context.Context, in access.ResolveInput) (access.Scope, error) {
	user, err := r.users.GetByID(ctx, in.UserID)
	if err != nil {
		return access.Scope{}, err
	}
	if user == nil || user.ID != in.UserID || user.OrganizationID <= 0 {
		return access.Scope{}, fmt.Errorf("account organization unavailable")
	}
	boundary, err := r.boundaries.ResolveViewerBoundary(ctx, user.OrganizationID)
	if err != nil {
		return access.Scope{}, err
	}
	if boundary.OrganizationID != user.OrganizationID {
		return access.Scope{}, fmt.Errorf("account organization mismatch")
	}
	scope, err := r.inner.Resolve(ctx, in)
	if err != nil {
		return access.Scope{}, err
	}
	if scope.UserID != user.ID {
		return access.Scope{}, fmt.Errorf("viewer account mismatch")
	}
	return boundary.Restrict(scope), nil
}

// Restrict applies the same ceiling to normal resolution and transaction snapshots.
func (b ViewerBoundary) Restrict(scope access.Scope) access.Scope {
	scope.AllowedLibraryIDs = intersectLibraries(b.AllowedLibraryIDs, scope.AllowedLibraryIDs)
	if len(scope.DisabledLibraryIDs) > 0 {
		scope.DisabledLibraryIDs = intersectLibraries(b.AllowedLibraryIDs, scope.DisabledLibraryIDs)
	}
	scope.LibrariesRestricted = true
	scope.OrganizationID = b.OrganizationID
	scope.OrganizationAccessRevision = b.AccessRevision
	return scope
}

func intersectLibraries(ceiling, requested []int) []int {
	out := make([]int, 0)
	for _, id := range ceiling {
		if requested == nil || slices.Contains(requested, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
