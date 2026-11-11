package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Full copies for the search mirror (spec §5 and §6). A copy is built from Postgres into a side
// index, "<prefix>_mirrorcopy_<unix seconds>", with the main index's settings and the main
// indexer's document builder, and is published under the main state row's index name in one
// step. Nothing of a half-finished copy is ever visible under that name, which matters because
// the mirror may become the main server halfway through a copy.
//
// A copy saves how far it got after each acknowledged batch and carries on after a restart. While
// a copy runs, the mirror applies no small changes: the copy's start snapshot becomes the bookmark
// when it is published, so the next follow applies everything that changed during the copy.

// The reasons searchMirrorNeedsCopy gives for a full copy. startCopy records the reason in the
// state, where the admin page shows it.
const (
	searchMirrorCopyReasonFirst         = "first_copy"
	searchMirrorCopyReasonMainRebuilt   = "main_rebuilt"
	searchMirrorCopyReasonPastCap       = "past_cap"
	searchMirrorCopyReasonForced        = "forced"
	searchMirrorCopyReasonNeedsFullCopy = "needs_full_copy"
)

// searchMirrorCopySideInfix names a copy's side index after the index prefix.
const searchMirrorCopySideInfix = "_mirrorcopy_"

// searchMirrorNeedsCopy reports whether the mirror must start a full copy, and why:
//   - "first_copy": the mirror has published nothing yet;
//   - "main_rebuilt": the main state row names another index or schema version;
//   - "past_cap": what the mirror holds (hold()) is older than maxLag, so the event cleanup no
//     longer keeps the rows it would need;
//   - "forced": ForceCopy is set (the manual task, a new or cloned mirror server, a restore);
//   - "needs_full_copy": the state says so.
//
// While a copy for the main's current index and schema is under way, ok is false: RunOnce
// continues that copy instead. The one exception is a copy that has itself gone past the cap,
// because the rows since its start may be pruned and publishing it would lose them; it starts
// again with "past_cap". A forced copy does not restart a copy already under way, which is as
// fresh. With no main index there is nothing to copy.
func searchMirrorNeedsCopy(row searchMirrorRow, main SearchIndexState, now time.Time, maxLag time.Duration) (string, bool) {
	if main.ActiveIndexUID == "" {
		return "", false
	}
	state := row.State
	pastCap := false
	if _, at, ok := state.hold(); ok && !at.IsZero() && maxLag > 0 && now.Sub(at) > maxLag {
		pastCap = true
	}
	if c := state.Copy; c != nil && c.TargetUID == main.ActiveIndexUID && c.SchemaVersion == main.SchemaVersion {
		if pastCap {
			return searchMirrorCopyReasonPastCap, true
		}
		return "", false
	}
	switch {
	case row.ActiveIndexUID == "":
		return searchMirrorCopyReasonFirst, true
	case row.ActiveIndexUID != main.ActiveIndexUID || row.SchemaVersion != main.SchemaVersion:
		return searchMirrorCopyReasonMainRebuilt, true
	case pastCap:
		return searchMirrorCopyReasonPastCap, true
	case state.ForceCopy:
		return searchMirrorCopyReasonForced, true
	case state.State == SearchMirrorNeedsFullCopy:
		return searchMirrorCopyReasonNeedsFullCopy, true
	}
	return "", false
}

// startCopy begins a full copy of main's index:
//  1. it takes a snapshot, which becomes the bookmark when the copy is published;
//  2. it deletes the side index of a copy that was under way;
//  3. it creates "<prefix>_mirrorcopy_<unix seconds of the database clock>" on the mirror with
//     the main index's settings, deleting a leftover of that name first so the side index is
//     always new (publishIndex tells a fresh copy by its createdAt);
//  4. it records the copy, sets State copying and drops a follow window, and saves.
//
// Copy.Total counts the documents the copy will hold. It is only shown, so a failed count leaves
// it 0. ForceCopy stays set until the copy is published, so a restart cannot lose it.
func (m *SearchMirror) startCopy(ctx context.Context, row *searchMirrorRow, main SearchIndexState, reason string) error {
	client := m.mirror
	prefix := m.startup.MeilisearchIndex

	snapshot, at, err := takeSearchMirrorSnapshot(ctx, m.pool)
	if err != nil {
		return searchMirrorDatabaseError(ctx, fmt.Errorf("start a search mirror copy: %w", err))
	}

	if previous := row.State.Copy; previous != nil && previous.SideUID != "" {
		if err := dropSearchMirrorCopyIndex(ctx, client, previous.SideUID); err != nil {
			return fmt.Errorf("start a search mirror copy: delete the earlier copy %s: %w", previous.SideUID, err)
		}
	}

	side := fmt.Sprintf("%s%s%d", prefix, searchMirrorCopySideInfix, at.Unix())
	exists, err := client.indexExists(ctx, side)
	if err != nil {
		return fmt.Errorf("start a search mirror copy: check %s: %w", side, err)
	}
	if exists {
		// Meilisearch 1.56 accepts creating an index that exists and fails the task, so a leftover
		// of the same name (a copy restarted within the same second) is deleted first.
		if err := dropSearchMirrorCopyIndex(ctx, client, side); err != nil {
			return fmt.Errorf("start a search mirror copy: delete the leftover %s: %w", side, err)
		}
	}
	ref, err := client.CreateIndex(ctx, side)
	if err == nil {
		err = client.WaitTask(ctx, ref)
	}
	if err != nil {
		return fmt.Errorf("start a search mirror copy: create %s: %w", side, err)
	}
	settings := catalogSearchMeilisearchSettings(m.startup.Embedder, m.startup.SemanticEnabled, m.startup.BinaryQuantized)
	ref, err = client.UpdateSettings(ctx, side, settings)
	if err == nil {
		err = client.WaitTask(ctx, ref)
	}
	if err != nil {
		return fmt.Errorf("start a search mirror copy: set the settings of %s: %w", side, err)
	}

	total := 0
	if m.pool != nil {
		total, err = countCatalogSearchEligibleDocuments(ctx, m.pool, m.startup.IndexTypes)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.WarnContext(ctx, "search mirror: could not count the documents for the copy's progress",
				"component", "catalog", "index", side, "err", err)
			total = 0
		}
	}

	row.State.Copy = &searchMirrorCopy{
		SideUID:       side,
		TargetUID:     main.ActiveIndexUID,
		SchemaVersion: main.SchemaVersion,
		Start:         snapshot,
		StartAt:       at,
		Total:         total,
		StartedAt:     at,
	}
	row.State.State = SearchMirrorCopying
	row.State.Reason = reason
	row.State.Window = nil
	if err := saveSearchMirrorRow(ctx, m.pool, *row); err != nil {
		return searchMirrorDatabaseError(ctx, err)
	}
	slog.InfoContext(ctx, "search mirror: full copy started",
		"component", "catalog", "index", side, "target", main.ActiveIndexUID, "reason", reason, "documents", total)
	return nil
}

// searchMirrorCopyBatch is one document batch sent to the side index and not yet acknowledged.
type searchMirrorCopyBatch struct {
	task   meilisearchTaskRef
	lastID string
	docs   int
}

// continueCopy carries the copy under way on until it is published or the deadline passes:
//  1. If the side index is gone (a new mirror server, or a restart that lost it), it starts the
//     copy again.
//  2. It pages through the catalog after Copy.AfterContentID with the live RebuildBatchSize,
//     sending payload-sized batches with at most CopyQueueDepth tasks in flight.
//  3. After each acknowledged batch it moves AfterContentID to that batch's last id, adds to Done,
//     sets LastSuccessAt, and saves. Tasks finish in the order they were sent, so everything up to
//     AfterContentID is in the side index.
//  4. When the pages run out it waits for every task and publishes the side index under
//     TargetUID. A side index publishIndex refuses as stale (errSearchMirrorSideStale: an
//     earlier publish swapped it already) starts the copy again.
//  5. The row then names TargetUID, its schema version and document count, and LastRebuildAt.
//  6. The copy's start snapshot becomes the bookmark, so the next follow applies what changed
//     during the copy; Copy, Window, Retry and ForceCopy are cleared.
//  7. The mirror's dead-letters are deleted: the copy holds those items as they are now.
//  8. Superseded indexes on the mirror are deleted (staleSearchMirrorIndexUIDs). A failure there
//     is only logged.
//  9. State becomes following.
//
// Steps 5 to 7 and 9 are saved in one transaction before step 8, so a published copy is recorded
// even when the cleanup is slow or the process stops during it. At the deadline it waits for the
// tasks in flight, records them, and returns; the next run carries on from there.
func (m *SearchMirror) continueCopy(ctx context.Context, row *searchMirrorRow, deadline time.Time) (bool, error) {
	if row.State.Copy == nil {
		return false, nil
	}
	client := m.mirror

	sideExists := false
	if side := row.State.Copy.SideUID; side != "" {
		exists, err := client.indexExists(ctx, side)
		if err != nil {
			return false, fmt.Errorf("continue the search mirror copy: check %s: %w", side, err)
		}
		sideExists = exists
	}
	if !sideExists {
		slog.WarnContext(ctx, "search mirror: the copy's side index is gone; starting the copy again",
			"component", "catalog", "index", row.State.Copy.SideUID, "target", row.State.Copy.TargetUID)
		if err := m.restartCopy(ctx, row); err != nil {
			return false, err
		}
	}
	c := row.State.Copy

	live, err := LoadCatalogSearchSettings(ctx, m.settings)
	if err != nil {
		return false, searchMirrorDatabaseError(ctx, fmt.Errorf("continue the search mirror copy: read the batch size: %w", err))
	}
	batchSize := live.RebuildBatchSize
	if batchSize <= 0 {
		batchSize = DefaultMeilisearchRebuildBatchSize
	}
	depth := m.opts.CopyQueueDepth
	if depth <= 0 {
		depth = searchMirrorCopyQueueDepth
	}

	queue := make([]searchMirrorCopyBatch, 0, depth)
	acknowledge := func() error {
		next := queue[0]
		queue = queue[1:]
		if err := client.WaitTask(ctx, next.task); err != nil {
			return fmt.Errorf("continue the search mirror copy: add documents to %s: %w", c.SideUID, err)
		}
		now := time.Now().UTC()
		c.AfterContentID = next.lastID
		c.Done += next.docs
		row.State.LastSuccessAt = &now
		row.State.State = SearchMirrorCopying
		row.State.LastError = ""
		row.State.ErrorSince = nil
		if err := saveSearchMirrorRow(ctx, m.pool, *row); err != nil {
			return searchMirrorDatabaseError(ctx, err)
		}
		return nil
	}
	drain := func() error {
		for len(queue) > 0 {
			if err := acknowledge(); err != nil {
				return err
			}
		}
		return nil
	}

	cursor := c.AfterContentID
	for {
		if !time.Now().Before(deadline) {
			return false, drain()
		}
		docs, err := m.docs.LoadDocumentsAfter(ctx, cursor, batchSize, m.startup.IndexTypes, m.startup.Embedder, m.startup.SemanticEnabled, m.startup.BinaryQuantized)
		if err != nil {
			return false, searchMirrorDatabaseError(ctx, fmt.Errorf("continue the search mirror copy: load documents: %w", err))
		}
		if len(docs) == 0 {
			break
		}
		for _, batch := range catalogSearchDocumentPayloadBatches(docs, m.opts.MaxPayloadBytes) {
			ref, err := client.AddDocuments(ctx, c.SideUID, batch)
			if err != nil {
				return false, fmt.Errorf("continue the search mirror copy: add documents to %s: %w", c.SideUID, err)
			}
			queue = append(queue, searchMirrorCopyBatch{task: ref, lastID: batch[len(batch)-1].ContentID, docs: len(batch)})
			for len(queue) >= depth {
				if err := acknowledge(); err != nil {
					return false, err
				}
			}
		}
		cursor = docs[len(docs)-1].ContentID
	}
	if err := drain(); err != nil {
		return false, err
	}

	// A publish that failed after its swap leaves the side index holding the target's old
	// contents; publishing it again would bring them back. publishIndex refuses such a side
	// index, which is not newer than the target (errSearchMirrorSideStale), and the copy starts
	// again instead.
	if err := client.publishIndex(ctx, c.SideUID, c.TargetUID); err != nil {
		if errors.Is(err, errSearchMirrorSideStale) {
			slog.WarnContext(ctx, "search mirror: the copy's side index is not newer than its target; starting the copy again",
				"component", "catalog", "index", c.SideUID, "target", c.TargetUID)
			return false, m.restartCopy(ctx, row)
		}
		return false, fmt.Errorf("continue the search mirror copy: %w", err)
	}

	previousUID := row.ActiveIndexUID
	documents, err := client.Stats(ctx, c.TargetUID)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		slog.WarnContext(ctx, "search mirror: published the copy, but could not read its document count",
			"component", "catalog", "index", c.TargetUID, "err", err)
		documents = c.Done
	}
	now := time.Now().UTC()
	startAt := c.StartAt
	row.ActiveIndexUID = c.TargetUID
	row.SchemaVersion = c.SchemaVersion
	row.DocumentCount = documents
	row.LastRebuildAt = &now
	row.State.Bookmark = c.Start
	row.State.BookmarkAt = &startAt
	row.State.Copy = nil
	row.State.Window = nil
	row.State.Retry = nil
	row.State.ForceCopy = false
	row.State.State = SearchMirrorFollowing
	row.State.Reason = ""
	row.State.LastSuccessAt = &now
	row.State.LastError = ""
	row.State.ErrorSince = nil
	err = pgx.BeginFunc(ctx, m.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM bloem_search_mirror_dead_letters`); err != nil {
			return fmt.Errorf("clear the search mirror dead-letters: %w", err)
		}
		return saveSearchMirrorRow(ctx, tx, *row)
	})
	if err != nil {
		return false, searchMirrorDatabaseError(ctx, fmt.Errorf("record the published search mirror copy: %w", err))
	}
	slog.InfoContext(ctx, "search mirror: full copy published",
		"component", "catalog", "index", row.ActiveIndexUID, "documents", documents)

	m.removeSupersededCopyIndexes(ctx, row.ActiveIndexUID, previousUID)
	return true, nil
}

// restartCopy starts the copy under way again from the beginning, for the same target and with
// the same reason.
func (m *SearchMirror) restartCopy(ctx context.Context, row *searchMirrorRow) error {
	c := row.State.Copy
	main := SearchIndexState{ActiveIndexUID: c.TargetUID, SchemaVersion: c.SchemaVersion}
	reason := row.State.Reason
	if reason == "" {
		reason = "copy_restarted"
	}
	return m.startCopy(ctx, row, main, reason)
}

// removeSupersededCopyIndexes deletes, on the mirror only, the indexes a published copy has made
// unreachable. Like cleanupSupersededMeilisearchIndexes it only logs a failure: the copy is
// published, a leftover costs disk, and the next copy's cleanup tries again.
func (m *SearchMirror) removeSupersededCopyIndexes(ctx context.Context, activeUID, previousUID string) {
	client := m.mirror
	uids, err := client.ListIndexUIDs(ctx)
	if err != nil {
		slog.WarnContext(ctx, "search mirror: could not list the mirror's indexes to remove superseded ones",
			"component", "catalog", "err", err)
		return
	}
	removed := 0
	for _, uid := range staleSearchMirrorIndexUIDs(uids, m.startup.MeilisearchIndex, activeUID, previousUID) {
		if err := dropSearchMirrorCopyIndex(ctx, client, uid); err != nil {
			slog.WarnContext(ctx, "search mirror: could not remove a superseded index",
				"component", "catalog", "index", uid, "removed", removed, "err", err)
			return
		}
		removed++
	}
}

// staleSearchMirrorIndexUIDs selects the mirror's indexes a published copy supersedes: this
// prefix's "_rebuild_" and "_mirrorcopy_" names and the previously published index, never the
// newly published one or the identity marker index. Every other index, including those of other
// prefixes, is kept.
func staleSearchMirrorIndexUIDs(uids []string, prefix, activeUID, previousUID string) []string {
	marker := searchMirrorMarkerIndex(prefix)
	hasPrefix := strings.TrimSpace(prefix) != ""
	rebuildPrefix := prefix + "_rebuild_"
	copyPrefix := prefix + searchMirrorCopySideInfix
	var stale []string
	for _, uid := range uids {
		if uid == "" || uid == activeUID || uid == marker {
			continue
		}
		if uid == previousUID ||
			(hasPrefix && (strings.HasPrefix(uid, rebuildPrefix) || strings.HasPrefix(uid, copyPrefix))) {
			stale = append(stale, uid)
		}
	}
	return stale
}

// dropSearchMirrorCopyIndex deletes uid on the mirror and waits for it. Meilisearch 1.56 accepts
// the deletion of a missing index and then fails the task with index_not_found, so a failed task
// counts as done when the index is gone.
func dropSearchMirrorCopyIndex(ctx context.Context, client *meilisearchClient, uid string) error {
	ref, err := client.DeleteIndex(ctx, uid)
	if err != nil {
		return err
	}
	if err := client.WaitTask(ctx, ref); err != nil {
		if ctx.Err() != nil {
			return err
		}
		if exists, existsErr := client.indexExists(ctx, uid); existsErr == nil && !exists {
			return nil
		}
		return err
	}
	return nil
}
