package storagesource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

func (r *Repository) NextDirectory(ctx context.Context, lease Lease) (Checkpoint, bool, error) {
	return r.NextDirectoryExcluding(ctx, lease, nil)
}

// NextDirectoryExcluding is NextDirectory without the directories a caller
// already has in flight, so several workers can list one run's directories.
// A false result only means no other directory is pending now: pages still
// in flight may queue more.
func (r *Repository) NextDirectoryExcluding(ctx context.Context, lease Lease, inFlight []string) (Checkpoint, bool, error) {
	var checkpoint Checkpoint
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return checkpoint, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	checkpoint, pending, err := pendingDirectoryTx(ctx, tx, lease, inFlight...)
	if err != nil {
		return checkpoint, false, err
	}
	return checkpoint, pending, tx.Commit(ctx)
}

// PagePublisher publishes a listed page to the catalog inside the transaction
// that records the page: its file entries and, for an incremental listing,
// the entries the provider reports removed. A failed publication rolls the
// page back, so the next attempt lists it again.
type PagePublisher func(ctx context.Context, tx pgx.Tx, files []*storagev1.Entry, removed []string) error

// ApplyPage records one listed page and advances its directory checkpoint.
// publish, when set, runs in the same transaction for the page's file entries.
func (r *Repository) ApplyPage(ctx context.Context, lease Lease, checkpoint Checkpoint, page *storagev1.ListResponse, publish PagePublisher) error {
	return r.applyPage(ctx, lease, checkpoint, page, nil, nil, publish)
}

// DirectorySkipper reports a listed directory its library never lists, as
// the filesystem scanner never walks into ignored folders. The directory's
// entry is still recorded; no scan directory is queued for it.
type DirectorySkipper func(*storagev1.Entry) bool

func (r *Repository) applyPage(ctx context.Context, lease Lease, checkpoint Checkpoint, page *storagev1.ListResponse, skip DirectorySkipper, authorize PageAuthorizer, publish PagePublisher) error {
	if checkpoint.Complete || !validText(checkpoint.DirectoryID, 1024, true) {
		return ErrCheckpointConflict
	}
	if err := mediasource.ValidatePage(checkpoint.Cursor, page); err != nil {
		return err
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(page)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	cursorHash := sha256.Sum256([]byte(checkpoint.Cursor))
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if authorize != nil {
		if err = authorize(ctx, tx); err != nil {
			return err
		}
	}
	if err = verifyLease(ctx, tx, lease); err != nil {
		return err
	}
	var current string
	var complete bool
	err = tx.QueryRow(ctx, `SELECT current_cursor,complete FROM bloem_storage_scan_directories WHERE run_id=$1 AND directory_id=$2 FOR UPDATE`, lease.RunID, checkpoint.DirectoryID).Scan(&current, &complete)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckpointConflict
	}
	if err != nil {
		return err
	}
	var savedCursor string
	var savedDigest []byte
	err = tx.QueryRow(ctx, `SELECT cursor,page_sha256 FROM bloem_storage_scan_cursors WHERE run_id=$1 AND directory_id=$2 AND cursor_sha256=$3`, lease.RunID, checkpoint.DirectoryID, cursorHash[:]).Scan(&savedCursor, &savedDigest)
	if err == nil {
		if savedCursor != checkpoint.Cursor || !bytes.Equal(savedDigest, digest[:]) {
			return ErrCheckpointConflict
		}
		// A replayed page verified the lease like any other page.
		if err = renewLeaseTx(ctx, tx, lease); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if complete || current != checkpoint.Cursor {
		return ErrCheckpointConflict
	}
	if !page.GetComplete() {
		nextHash := sha256.Sum256([]byte(page.GetNextCursor()))
		var historical string
		err = tx.QueryRow(ctx, `SELECT cursor FROM bloem_storage_scan_cursors WHERE run_id=$1 AND directory_id=$2 AND cursor_sha256=$3`, lease.RunID, checkpoint.DirectoryID, nextHash[:]).Scan(&historical)
		if err == nil {
			return fmt.Errorf("storage discovery cursor repeated or hash collided")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	files, shrunk, err := applyLocationEntries(ctx, tx, lease, checkpoint, page, skip)
	if err != nil {
		return err
	}
	if publish != nil {

		removed := make([]string, 0, len(page.RemovedEntryIds))
		for _, id := range page.RemovedEntryIds {
			var retained bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_storage_location_edges WHERE location_id=$1 AND entry_id=$2)`, lease.LocationID, id).Scan(&retained); err != nil {
				return err
			}
			if !retained {
				removed = append(removed, id)
			}
		}
		if len(files) > 0 || len(removed) > 0 || page.GetComplete() {
			if err = publish(ctx, tx, files, removed); err != nil {
				return err
			}
		}
	}
	if page.GetComplete() && page.GetChangeToken() != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_storage_change_tokens(source_key,directory_id,token,configuration_revision,issued_at,location_id,selection_revision)
 VALUES($1,$2,$3,$4,now(),$5,$6)
 ON CONFLICT(location_id,directory_id) DO UPDATE SET token=EXCLUDED.token,configuration_revision=EXCLUDED.configuration_revision,selection_revision=EXCLUDED.selection_revision,issued_at=EXCLUDED.issued_at`,
			lease.SourceKey, checkpoint.DirectoryID, page.GetChangeToken(), lease.ConfigurationRevision, lease.LocationID, lease.SelectionRevision); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO bloem_storage_scan_cursors(run_id,directory_id,cursor_sha256,cursor,page_sha256) VALUES($1,$2,$3,$4,$5)`, lease.RunID, checkpoint.DirectoryID, cursorHash[:], checkpoint.Cursor, digest[:]); err != nil {
		return err
	}
	// The first page decides whether this run lists the directory in full.
	if _, err = tx.Exec(ctx, `UPDATE bloem_storage_scan_directories SET current_cursor=$3,complete=$4,processed_count=processed_count+$5,
 full_listing=(listing_mode='full') WHERE run_id=$1 AND directory_id=$2`,
		lease.RunID, checkpoint.DirectoryID, page.GetNextCursor(), page.GetComplete(), len(page.GetEntries())); err != nil {
		return err
	}
	if page.GetComplete() {
		if _, err = tx.Exec(ctx, `UPDATE bloem_storage_location_directories SET last_complete_run=$3,last_listing_mode=(SELECT listing_mode FROM bloem_storage_scan_directories WHERE run_id=$3 AND directory_id=$2) WHERE location_id=$1 AND directory_id=$2`, lease.LocationID, checkpoint.DirectoryID, lease.RunID); err != nil {
			return err
		}
	}
	listed := make([]string, 0, len(page.GetEntries()))
	for _, e := range page.GetEntries() {
		listed = append(listed, e.GetId())
	}
	if err = propagatePageRootsTx(ctx, tx, lease, pageReach{directoryID: checkpoint.DirectoryID, entries: listed, opened: checkpoint.Cursor == "", closed: page.GetComplete(), shrunk: shrunk}); err != nil {
		return err
	}
	if err = verifyLease(ctx, tx, lease); err != nil {
		return err
	}
	// The page retained the scan's authority and verified its lease under the
	// locks a renewal takes, so its commit renews the lease. Page work holding
	// the library lock therefore cannot starve the lease of renewals.
	if err = renewLeaseTx(ctx, tx, lease); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
