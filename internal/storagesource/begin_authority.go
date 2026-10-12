package storagesource

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// BeginAuthorizer retains current resource authority before the journal
// acquires its source and run locks, through lease creation or takeover.
type BeginAuthorizer func(context.Context, pgx.Tx) error

// BeginAuthorized begins or resumes discovery under retained resource
// authority. The required authorizer acquires installation before source
// locks. Standalone discovery may use Begin without library authority.
func (r *Repository) BeginAuthorized(ctx context.Context, sourceKey uuid.UUID, owner string, ttl time.Duration, authorize BeginAuthorizer) (Lease, error) {
	if authorize == nil {
		return Lease{SourceKey: sourceKey, Owner: owner}, fmt.Errorf("storage begin authority required")
	}
	return r.begin(ctx, sourceKey, owner, ttl, authorize)
}

// BeginLocationAuthorized binds discovery to one exact retained selection.
// Its authorizer must retain the complete library aggregate before journal locks.
func (r *Repository) BeginLocationAuthorized(ctx context.Context, location Location, owner string, ttl time.Duration, authorize BeginAuthorizer) (Lease, error) {
	if authorize == nil || location.ID == uuid.Nil || location.SourceKey == uuid.Nil || location.FolderID <= 0 || location.SelectionRevision <= 0 {
		return Lease{}, fmt.Errorf("storage location begin authority required")
	}
	return r.beginLocation(ctx, location.SourceKey, owner, ttl, authorize, &location)
}
