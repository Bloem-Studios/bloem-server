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

// bloemOwnsFolderReconcile routes Silo's ReconcileFolderMembership through
// Bloem's reconciliation (below). Bloem's version differs from Silo's in
// ways that are not expressible as a hook inside Silo's body: it runs in a
// caller-supplied transaction (ReconcileFolderMembershipTx /
// ReconcileContentMembershipTx) so presence changes and catalog cleanup are
// atomic, can narrow to one content item, locks orphan candidates in a
// deterministic order before deciding, never deletes an item that still has
// a present file anywhere, and only reports image dirs for rows the guarded
// DELETE actually removed. Returning false restores Silo's behavior.
func bloemOwnsFolderReconcile() bool { return true }

// reconcileFolderMembershipAndCommit is ReconcileFolderMembership's Bloem
// path: reconcile inside the transaction Silo's method opened, then commit.
func (r *LibraryItemRepository) reconcileFolderMembershipAndCommit(
	ctx context.Context,
	tx pgx.Tx,
	folderID int,
	protectedPathPrefixes []string,
) (int, int, []string, error) {
	removed, deleted, orphanedImageDirs, err := r.ReconcileFolderMembershipTx(ctx, tx, folderID, protectedPathPrefixes)
	if err != nil {
		return 0, 0, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, nil, fmt.Errorf("committing membership reconciliation transaction: %w", err)
	}

	return removed, deleted, orphanedImageDirs, nil
}

func (r *LibraryItemRepository) reconcileFolderMembershipTx(
	ctx context.Context,
	tx pgx.Tx,
	folderID int,
	contentIDs []string,
	protectedPathPrefixes []string,
) (int, int, []string, error) {
	args := []any{folderID}
	contentPredicate := ""
	if len(contentIDs) > 0 {
		args = append(args, contentIDs)
		contentPredicate = "\n\t\t  AND mil.content_id = ANY($2::text[])"
	}

	// Manga series items (type='manga') are virtual parents with no media_file of
	// their own — their membership is keyed to having chapters, not files. Exclude
	// them here so file-presence reconciliation never sweeps a live series; orphan
	// series (no remaining chapters) are cleaned up separately by the manga scan.
	rows, err := tx.Query(ctx, `
		DELETE FROM media_item_libraries mil
		WHERE mil.media_folder_id = $1`+contentPredicate+`
		  AND NOT EXISTS (
			SELECT 1
			FROM media_files mf
			WHERE mf.media_folder_id = mil.media_folder_id
			  AND mf.content_id = mil.content_id
			  AND mf.missing_since IS NULL
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM media_items mi
			WHERE mi.content_id = mil.content_id
			  AND mi.type = 'manga'
		  )
		RETURNING mil.content_id
	`, args...)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("deleting stale folder memberships: %w", err)
	}
	defer rows.Close()

	removedContentIDs := make([]string, 0)
	for rows.Next() {
		var contentID string
		if err := rows.Scan(&contentID); err != nil {
			return 0, 0, nil, fmt.Errorf("scanning removed folder membership: %w", err)
		}
		removedContentIDs = append(removedContentIDs, contentID)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, nil, fmt.Errorf("iterating removed folder memberships: %w", err)
	}
	rows.Close()

	deletedItems := 0
	var orphanedImageDirs []string
	// Find both newly orphaned items and items preserved by an earlier
	// protected-root pass. The latter no longer have a membership to return from
	// the DELETE above, but their surviving media_files row still ties them to
	// this folder so they can be reconsidered after the root recovers.
	orphanIDs, err := collectOrphanIDs(ctx, tx, removedContentIDs)
	if err != nil {
		return 0, 0, nil, err
	}
	previouslyProtected, err := collectScopedFolderFileOrphanIDs(ctx, tx, folderID, contentIDs)
	if err != nil {
		return 0, 0, nil, err
	}
	orphanIDs = appendUniqueStrings(orphanIDs, previouslyProtected...)
	if len(orphanIDs) > 0 {
		// Exempt orphans whose files sit under an unreachable root: the files
		// still exist, the root is just offline. See the doc comment above.
		if len(protectedPathPrefixes) > 0 {
			orphanIDs, err = excludeOrphansUnderProtectedPrefixes(ctx, tx, orphanIDs, folderID, protectedPathPrefixes)
			if err != nil {
				return 0, 0, nil, err
			}
		}

		if len(orphanIDs) > 0 {
			// Every writer that establishes item-owned state locks media_items
			// first (INSERT/UPDATE, or an FK key-share lock for membership). Lock
			// candidate rows in one deterministic order so cross-folder refreshes
			// either become visible before the orphan decision or wait until after
			// a legitimate delete. The orphan decision deliberately happens in a
			// separate statement: at READ COMMITTED it receives a fresh snapshot
			// after any conflicting writer we waited for has committed.
			orphanIDs, err = lockMediaItemCandidates(ctx, tx, orphanIDs)
			if err != nil {
				return 0, 0, nil, err
			}
		}

		if len(orphanIDs) > 0 {
			orphanIDs, err = collectGloballyDeletableMediaItemIDs(ctx, tx, orphanIDs)
			if err != nil {
				return 0, 0, nil, err
			}
		}

		if len(orphanIDs) > 0 {
			// Capture paths before cascades remove their owners, but do not return
			// any cleanup prefix until the guarded DELETE says which rows really
			// disappeared. A concurrent survivor must keep its artwork.
			rawImageDirs, err := collectRawImageDirs(ctx, tx, orphanIDs)
			if err != nil {
				return 0, 0, nil, err
			}
			rows, err := tx.Query(ctx, `
				DELETE FROM media_items mi
				WHERE mi.content_id = ANY($1)
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
				RETURNING mi.content_id
			`, orphanIDs)
			if err != nil {
				return 0, 0, nil, fmt.Errorf("deleting orphaned media items after folder reconciliation: %w", err)
			}
			deletedContentIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return 0, 0, nil, fmt.Errorf("collecting deleted orphaned media item IDs: %w", err)
			}
			deletedItems = len(deletedContentIDs)
			orphanedImageDirs, err = filterUnreferencedImageDirs(ctx, tx, rawImageDirs, deletedContentIDs)
			if err != nil {
				return 0, 0, nil, err
			}
			if err := EnqueueSearchIndexDeletes(ctx, tx, deletedContentIDs); err != nil {
				return 0, 0, nil, fmt.Errorf("enqueueing catalog search orphan deletes: %w", err)
			}
		}
	}

	return len(removedContentIDs), deletedItems, orphanedImageDirs, nil
}

func collectScopedFolderFileOrphanIDs(ctx context.Context, tx pgx.Tx, folderID int, contentIDs []string) ([]string, error) {
	args := []any{folderID}
	contentPredicate := ""
	if len(contentIDs) > 0 {
		args = append(args, contentIDs)
		contentPredicate = "\n\t\t  AND mf.content_id = ANY($2::text[])"
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT mf.content_id
		FROM media_files mf
		WHERE mf.media_folder_id = $1
		  AND mf.content_id IS NOT NULL
		  AND mf.content_id <> ''`+contentPredicate+`
		  AND NOT EXISTS (
			SELECT 1 FROM media_item_libraries mil WHERE mil.content_id = mf.content_id
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM media_files active
			WHERE active.media_folder_id = mf.media_folder_id
			  AND active.content_id = mf.content_id
			  AND active.missing_since IS NULL
		  )
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("finding previously protected folder orphans: %w", err)
	}
	defer rows.Close()
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("collecting previously protected folder orphans: %w", err)
	}
	return ids, nil
}
