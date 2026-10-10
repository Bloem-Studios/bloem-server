package storagesource

import (
	"context"
	"fmt"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
)

// PageAuthorizer checks the attached library's current authority and keeps
// its resource locks through the page's commit. It runs before journal locks
// and on replay, so a retained lease or saved cursor cannot grant authority.
type PageAuthorizer func(context.Context, pgx.Tx) error

// ApplyPageAuthorized records and publishes a page under current resource
// authority. The required authorizer must acquire installation/source locks
// in their established order before the journal locks its source and run.
// Standalone discovery may use ApplyPage without catalog authority.
func (r *Repository) ApplyPageAuthorized(ctx context.Context, lease Lease, checkpoint Checkpoint, page *storagev1.ListResponse, authorize PageAuthorizer, publish PagePublisher) error {
	if authorize == nil {
		return fmt.Errorf("storage page authority required")
	}
	return r.applyPage(ctx, lease, checkpoint, page, authorize, publish)
}
