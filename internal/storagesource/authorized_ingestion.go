package storagesource

import (
	"context"
	"errors"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

// ErrIngestionAuthorizationRequired prevents publication without a host policy.
var ErrIngestionAuthorizationRequired = errors.New("storage ingestion authorization required")

// PublishAuthorizedIngestion atomically authorizes, publishes, and advances the
// durable claim. authorize is mandatory and runs FIRST, before source/run locks,
// so the host can take installation -> source locks in registry order and retain
// its binding/folder/owner/organization/entitlement fences through commit.
// Both callbacks are SQL-only: they must not do network I/O or commit/rollback.
// A nil publish acknowledges an unsupported entry after full authorization.
// Ordinary PublishIngestion remains unchanged.
func (r *Repository) PublishAuthorizedIngestion(ctx context.Context, claim IngestionClaim, authorize func(context.Context, pgx.Tx) error, publish func(context.Context, pgx.Tx, *storagev1.Entry) error) error {
	if authorize == nil {
		return ErrIngestionAuthorizationRequired
	}
	if claim.Entry == nil || claim.Token == uuid.Nil {
		return ErrCheckpointConflict
	}
	if r == nil || r.pool == nil {
		return ErrSourceUnavailable
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = authorize(ctx, tx); err != nil {
		return err
	}
	l := claim.Lease
	checkpoint, err := verifyIngestionLease(ctx, tx, l)
	if err != nil {
		return err
	}
	if checkpoint.complete || checkpoint.token == nil || *checkpoint.token != claim.Token ||
		checkpoint.pending != claim.Entry.GetId() || checkpoint.revision != claim.Entry.GetRevision() {
		return ErrCheckpointConflict
	}
	entry, err := loadIngestionEntry(ctx, tx, l, checkpoint.pending)
	if err != nil {
		return err
	}
	if !proto.Equal(entry, claim.Entry) {
		return ErrCheckpointConflict
	}
	if publish != nil {
		if err = publish(ctx, tx, entry); err != nil {
			return err
		}
	}
	if err = ingestionLeaseActive(ctx, tx, l); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE bloem_storage_ingestion SET last_entry_id=pending_entry_id,pending_entry_id='',pending_revision='',pending_token=NULL WHERE run_id=$1 AND binding_id=$2", l.RunID, l.BindingID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
