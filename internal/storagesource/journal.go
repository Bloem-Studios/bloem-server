package storagesource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	storagev1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

func (r *Repository) NextDirectory(ctx context.Context, lease Lease) (Checkpoint, bool, error) {
	var checkpoint Checkpoint
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return checkpoint, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = verifyLease(ctx, tx, lease); err != nil {
		return checkpoint, false, err
	}
	err = tx.QueryRow(ctx, `SELECT directory_id,current_cursor,complete FROM bloem_storage_scan_directories WHERE run_id=$1 AND NOT complete ORDER BY directory_id LIMIT 1`, lease.RunID).Scan(&checkpoint.DirectoryID, &checkpoint.Cursor, &checkpoint.Complete)
	if errors.Is(err, pgx.ErrNoRows) {
		return checkpoint, false, tx.Commit(ctx)
	}
	if err != nil {
		return checkpoint, false, err
	}
	return checkpoint, true, tx.Commit(ctx)
}

func (r *Repository) ApplyPage(ctx context.Context, lease Lease, checkpoint Checkpoint, page *storagev1.ListResponse) error {
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
	if err = applyEntries(ctx, tx, lease, page.GetEntries()); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO bloem_storage_scan_cursors(run_id,directory_id,cursor_sha256,cursor,page_sha256) VALUES($1,$2,$3,$4,$5)`, lease.RunID, checkpoint.DirectoryID, cursorHash[:], checkpoint.Cursor, digest[:]); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_storage_scan_directories SET current_cursor=$3,complete=$4,processed_count=processed_count+$5 WHERE run_id=$1 AND directory_id=$2`, lease.RunID, checkpoint.DirectoryID, page.GetNextCursor(), page.GetComplete(), len(page.GetEntries())); err != nil {
		return err
	}
	if err = verifyLease(ctx, tx, lease); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func applyEntries(ctx context.Context, tx pgx.Tx, lease Lease, entries []*storagev1.Entry) error {
	// This batch is bounded by the validated 512-entry page, not library size.
	batch := &pgx.Batch{}
	for _, entry := range entries {
		batch.Queue(`INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run)
  VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
  ON CONFLICT(source_key,entry_id) DO UPDATE SET name=EXCLUDED.name,logical_path=EXCLUDED.logical_path,kind=EXCLUDED.kind,size=EXCLUDED.size,modified_unix_nano=EXCLUDED.modified_unix_nano,revision=EXCLUDED.revision,configuration_revision=EXCLUDED.configuration_revision,last_seen_run=EXCLUDED.last_seen_run,absence_confirmed_at=NULL
  WHERE bloem_storage_entries.last_seen_run<>EXCLUDED.last_seen_run`, lease.SourceKey, entry.GetId(), entry.GetName(), entry.GetLogicalPath(), int32(entry.GetKind()), entry.GetSize(), entry.GetModifiedUnixNano(), entry.GetRevision(), lease.ConfigurationRevision, lease.RunID)
	}
	results := tx.SendBatch(ctx, batch)
	for range entries {
		tag, err := results.Exec()
		if err != nil {
			_ = results.Close()
			return err
		}
		if tag.RowsAffected() != 1 {
			_ = results.Close()
			return fmt.Errorf("storage entry identity repeated within generation")
		}
	}
	if err := results.Close(); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.GetKind() == storagev1.EntryKind_ENTRY_KIND_DIRECTORY {
			if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_scan_directories(run_id,directory_id) VALUES($1,$2)`, lease.RunID, entry.GetId()); err != nil {
				return err
			}
		}
	}
	return nil
}
