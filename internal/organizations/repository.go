// Package organizations owns the organization boundary around Silo's library policy.
package organizations

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type Queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Repository accepts either a pool or the caller's transaction.
type Repository struct{ db Queryer }

func NewRepository(db Queryer) *Repository { return &Repository{db: db} }

// ViewerBoundary is an explicit ceiling. An empty library set grants no access.
type ViewerBoundary struct {
	OrganizationID    int64
	AccessRevision    int64
	AllowedLibraryIDs []int
}

// ResolveViewerBoundary loads activity and library authority in one snapshot.
// Within a transaction the organization lock serializes this read with revocation.
func (r *Repository) ResolveViewerBoundary(ctx context.Context, organizationID int64) (ViewerBoundary, error) {
	var b ViewerBoundary
	err := r.db.QueryRow(ctx, `
 SELECT o.id, o.access_revision, ARRAY(
   SELECT f.id FROM media_folders f WHERE f.organization_id=o.id
   UNION
   SELECT g.media_folder_id FROM organization_library_grants g
     JOIN media_folders f ON f.id=g.media_folder_id AND f.organization_id IS NULL
     WHERE g.organization_id=o.id
   ORDER BY 1
 ) FROM organizations o WHERE o.id=$1 AND o.status='active' FOR SHARE OF o`, organizationID).Scan(&b.OrganizationID, &b.AccessRevision, &b.AllowedLibraryIDs)
	if err != nil {
		return ViewerBoundary{}, fmt.Errorf("resolve organization library boundary: %w", err)
	}
	if b.AllowedLibraryIDs == nil {
		b.AllowedLibraryIDs = []int{}
	}
	return b, nil
}
