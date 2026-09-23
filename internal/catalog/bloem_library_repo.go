package catalog

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ReconcileFolderMembershipTx performs folder-wide membership and orphan
// reconciliation in the caller's transaction. Callers that mutate file
// presence must use this form so the presence change and resulting catalog
// cleanup are atomic.
func (r *LibraryItemRepository) ReconcileFolderMembershipTx(
	ctx context.Context,
	tx pgx.Tx,
	folderID int,
	protectedPathPrefixes []string,
) (int, int, []string, error) {
	return r.reconcileFolderMembershipTx(ctx, tx, folderID, nil, protectedPathPrefixes)
}

// ReconcileContentMembershipTx narrows membership and orphan reconciliation
// to one content item in the caller's transaction. It is used by single-file
// events so an event under one root cannot purge deliberately preserved
// orphans under an unrelated root in the same folder.
func (r *LibraryItemRepository) ReconcileContentMembershipTx(
	ctx context.Context,
	tx pgx.Tx,
	folderID int,
	contentID string,
	protectedPathPrefixes []string,
) (int, int, []string, error) {
	contentID = strings.TrimSpace(contentID)
	if contentID == "" {
		return 0, 0, nil, nil
	}
	return r.reconcileFolderMembershipTx(ctx, tx, folderID, []string{contentID}, protectedPathPrefixes)
}

// lockMediaItemCandidates takes row locks in content-ID order. The explicit
// ordering is required because two folder reconciliations can share more than
// one item while holding different folder advisory locks.
func lockMediaItemCandidates(ctx context.Context, tx pgx.Tx, contentIDs []string) ([]string, error) {
	if len(contentIDs) == 0 {
		return nil, nil
	}
	contentIDs = append([]string(nil), contentIDs...)
	slices.Sort(contentIDs)
	contentIDs = slices.Compact(contentIDs)
	rows, err := tx.Query(ctx, `
		SELECT mi.content_id
		FROM media_items mi
		WHERE mi.content_id = ANY($1::text[])
		ORDER BY mi.content_id
		FOR UPDATE
	`, contentIDs)
	if err != nil {
		return nil, fmt.Errorf("locking orphaned media item candidates: %w", err)
	}
	lockedIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("collecting locked orphaned media item candidates: %w", err)
	}
	return lockedIDs, nil
}

// collectGloballyDeletableMediaItemIDs is intentionally separate from the row
// lock statement. Under READ COMMITTED this fresh statement observes a
// cross-folder refresh that committed while candidate locking was blocked.
func collectGloballyDeletableMediaItemIDs(ctx context.Context, tx pgx.Tx, contentIDs []string) ([]string, error) {
	if len(contentIDs) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT mi.content_id
		FROM media_items mi
		WHERE mi.content_id = ANY($1::text[])
		  AND NOT EXISTS (
			SELECT 1
			FROM media_item_libraries mil
			WHERE mil.content_id = mi.content_id
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM media_files mf
			WHERE mf.content_id = mi.content_id
			  AND mf.missing_since IS NULL
		  )
		ORDER BY mi.content_id
	`, contentIDs)
	if err != nil {
		return nil, fmt.Errorf("rechecking global orphaned media item state: %w", err)
	}
	deletableIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("collecting globally orphaned media item IDs: %w", err)
	}
	return deletableIDs, nil
}
