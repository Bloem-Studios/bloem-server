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
//   - "past_cap": what the mirror holds is older than maxLag, measured as the cleanup measures it
//     (holdSince), so the cleanup no longer keeps the rows it would need;
//   - "forced": ForceCopy is set (the manual task, a new or cloned mirror server, a restore);
//   - "needs_full_copy": the state says so.
//
// While a copy for the main's current index and schema is under way, ok is false: RunOnce
// continues that copy instead. The one exception is a copy that made no progress for longer
// than the cap, because the rows since its start may be pruned and publishing it would lose
// them; it starts again with "past_cap". A copy that keeps making progress is never past the
// cap, however long it takes. A forced copy does not restart a copy already under way, which is
// as fresh. A copy for an index the main no longer names is replaced. With no main index there
// is nothing to copy.
func searchMirrorNeedsCopy(row searchMirrorRow, main SearchIndexState, now time.Time, maxLag time.Duration) (string, bool) {
	if main.ActiveIndexUID == "" {
		return "", false
	}
	state := row.State
	pastCap := false
	if _, since, ok := row.holdSince(); ok && !since.IsZero() && maxLag > 0 && now.Sub(since) > maxLag {
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

	if err := m.checkOwner(ctx); err != nil {
		return err
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
				"component", "catalog", "index", side, "err", m.errorText(err))
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
		ProgressAt:    at,
	}
	row.State.State = SearchMirrorCopying
	row.State.Reason = reason
	row.State.Window = nil
	if err := saveSearchMirrorRow(ctx, m.pool, row); err != nil {
		return searchMirrorDatabaseError(ctx, err)
	}
	slog.InfoContext(ctx, "search mirror: full copy started",
		"component", "catalog", "index", side, "target", main.ActiveIndexUID, "reason", reason, "documents", total)
	return nil
}

// searchMirrorCopyBatch is one document batch sent to the side index and not yet acknowledged.
type searchMirrorCopyBatch struct {
	task   meilisearchTaskRef
	ids    []string
	lastID string
}

// continueCopy carries the copy under way on until it is published or the deadline passes:
//  1. If the side index is gone (a new mirror server, or a restart that lost it), it starts the
//     copy again.
//  2. It pages through the catalog after Copy.AfterContentID with the live RebuildBatchSize,
//     sending payload-sized batches with at most CopyQueueDepth tasks in flight.
//  3. After each acknowledged batch it moves AfterContentID to that batch's last id, adds to Done,
//     sets LastSuccessAt and Copy.ProgressAt, and saves. Tasks finish in the order they were sent,
//     so everything up to AfterContentID is in the side index. A batch Meilisearch refuses is
//     split (splitApply) against the side index: the refused documents are dead-lettered and the
//     copy goes on; only a batch of two or more with nothing accepted fails the copy.
//  4. When the pages run out it waits for every task and publishes the side index under
//     TargetUID (publishCopy).
//
// At the deadline it waits for the tasks in flight, records them, and returns; the next run
// carries on from there. Before every write to the mirror it checks that the run still holds
// the mirror's lock.
func (m *SearchMirror) continueCopy(ctx context.Context, row *searchMirrorRow, deadline time.Time) (bool, error) {
	if row.State.Copy == nil {
		return false, nil
	}
	if err := m.ensureCopySide(ctx, row); err != nil {
		return false, err
	}
	run, err := m.newSearchMirrorCopyRun(ctx, row)
	if err != nil {
		return false, err
	}
	finished, err := run.sendAll(ctx, deadline)
	if err != nil || !finished {
		return false, err
	}
	return m.publishCopy(ctx, row)
}

// ensureCopySide starts the copy again when its side index is gone.
func (m *SearchMirror) ensureCopySide(ctx context.Context, row *searchMirrorRow) error {
	side := row.State.Copy.SideUID
	if side != "" {
		exists, err := m.mirror.indexExists(ctx, side)
		if err != nil {
			return fmt.Errorf("continue the search mirror copy: check %s: %w", side, err)
		}
		if exists {
			return nil
		}
	}
	slog.WarnContext(ctx, "search mirror: the copy's side index is gone; starting the copy again",
		"component", "catalog", "index", side, "target", row.State.Copy.TargetUID)
	return m.restartCopy(ctx, row)
}

// searchMirrorCopyRun is one run's part of a copy: the batches in flight and where they end.
type searchMirrorCopyRun struct {
	m         *SearchMirror
	row       *searchMirrorRow
	batchSize int
	depth     int
	queue     []searchMirrorCopyBatch
}

func (m *SearchMirror) newSearchMirrorCopyRun(ctx context.Context, row *searchMirrorRow) (*searchMirrorCopyRun, error) {
	live, err := LoadCatalogSearchSettings(ctx, m.settings)
	if err != nil {
		return nil, searchMirrorDatabaseError(ctx, fmt.Errorf("continue the search mirror copy: read the batch size: %w", err))
	}
	run := &searchMirrorCopyRun{m: m, row: row, batchSize: live.RebuildBatchSize, depth: m.opts.CopyQueueDepth}
	if run.batchSize <= 0 {
		run.batchSize = DefaultMeilisearchRebuildBatchSize
	}
	if run.depth <= 0 {
		run.depth = searchMirrorCopyQueueDepth
	}
	return run, nil
}

// sendAll sends the catalog after the copy's cursor until it runs out (finished) or the
// deadline passes, then waits for every batch in flight.
func (r *searchMirrorCopyRun) sendAll(ctx context.Context, deadline time.Time) (finished bool, err error) {
	m, c := r.m, r.row.State.Copy
	cursor := c.AfterContentID
	for {
		if !time.Now().Before(deadline) {
			return false, r.drain(ctx)
		}
		docs, err := m.docs.LoadDocumentsAfter(ctx, cursor, r.batchSize, m.startup.IndexTypes, m.startup.Embedder, m.startup.SemanticEnabled, m.startup.BinaryQuantized)
		if err != nil {
			return false, searchMirrorDatabaseError(ctx, fmt.Errorf("continue the search mirror copy: load documents: %w", err))
		}
		if len(docs) == 0 {
			return true, r.drain(ctx)
		}
		for _, batch := range catalogSearchDocumentPayloadBatches(docs, m.opts.MaxPayloadBytes) {
			if err := r.send(ctx, batch); err != nil {
				return false, err
			}
		}
		cursor = docs[len(docs)-1].ContentID
	}
}

// send adds one batch to the side index, then acknowledges batches until fewer than depth are
// in flight.
func (r *searchMirrorCopyRun) send(ctx context.Context, batch []catalogSearchDocument) error {
	side := r.row.State.Copy.SideUID
	if err := r.m.checkOwner(ctx); err != nil {
		return err
	}
	ref, err := r.m.mirror.AddDocuments(ctx, side, batch)
	if err != nil {
		return fmt.Errorf("continue the search mirror copy: add documents to %s: %w", side, err)
	}
	ids := make([]string, len(batch))
	for i, doc := range batch {
		ids[i] = doc.ContentID
	}
	r.queue = append(r.queue, searchMirrorCopyBatch{task: ref, ids: ids, lastID: ids[len(ids)-1]})
	for len(r.queue) >= r.depth {
		if err := r.acknowledge(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (r *searchMirrorCopyRun) drain(ctx context.Context) error {
	for len(r.queue) > 0 {
		if err := r.acknowledge(ctx); err != nil {
			return err
		}
	}
	return nil
}

// acknowledge waits for the oldest batch in flight and records it. A batch Meilisearch refused
// is split first (splitCopyBatch).
func (r *searchMirrorCopyRun) acknowledge(ctx context.Context) error {
	m, row, c := r.m, r.row, r.row.State.Copy
	next := r.queue[0]
	r.queue = r.queue[1:]
	if err := m.mirror.WaitTask(ctx, next.task); err != nil {
		if searchMirrorNotRecorded(ctx, err) || classifySearchMirrorError(err) != searchMirrorErrRejected {
			return fmt.Errorf("continue the search mirror copy: add documents to %s: %w", c.SideUID, err)
		}
		if err := m.splitCopyBatch(ctx, c.SideUID, next.ids, err); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	c.AfterContentID = next.lastID
	c.Done += len(next.ids)
	c.ProgressAt = now
	row.State.LastSuccessAt = &now
	row.State.State = SearchMirrorCopying
	row.State.LastError = ""
	row.State.ErrorSince = nil
	if err := saveSearchMirrorRow(ctx, m.pool, row); err != nil {
		return searchMirrorDatabaseError(ctx, err)
	}
	return nil
}

// splitCopyBatch handles a copy batch the side index refused (refusal), as pageFailed handles a
// page on its last try: the batch is split down to the documents Meilisearch itself refuses,
// which are dead-lettered, and the copy goes on. They stay dead-lettered when the copy is
// published. A split stopped by another kind of error returns it; a batch of two or more with
// nothing accepted returns the refusal, and the mirror is failing.
func (m *SearchMirror) splitCopyBatch(ctx context.Context, side string, ids []string, refusal error) error {
	accepted, refusals, abort := m.splitApply(ctx, m.mirror, side, ids)
	if abort != nil {
		return abort
	}
	if len(accepted) == 0 && len(ids) > 1 {
		return fmt.Errorf("continue the search mirror copy: %s refused every document of a batch: %w", side, refusal)
	}
	if err := m.writeDeadLetters(ctx, refusals); err != nil {
		return searchMirrorDatabaseError(ctx, err)
	}
	m.logDeadLettered(ctx, accepted, refusals)
	return nil
}

// publishCopy publishes the finished side index under TargetUID and records it:
//  1. A side index publishIndex refuses as stale (errSearchMirrorSideStale: an earlier publish
//     swapped it already) starts the copy again.
//  2. The row then names TargetUID, its schema version and document count, and LastRebuildAt.
//  3. The copy's start snapshot becomes the bookmark, so the next follow applies what changed
//     during the copy; Copy, Window, Retry and ForceCopy are cleared.
//  4. The dead letters from before the copy are deleted: the copy holds those items as they are
//     now. Those the copy itself refused stay.
//  5. State becomes following.
//  6. Superseded indexes on the mirror are deleted (staleSearchMirrorIndexUIDs). A failure there
//     is only logged.
//
// Steps 2 to 5 are saved in one transaction before step 6, so a published copy is recorded even
// when the cleanup is slow or the process stops during it.
func (m *SearchMirror) publishCopy(ctx context.Context, row *searchMirrorRow) (bool, error) {
	client, c := m.mirror, row.State.Copy
	// A publish that failed after its swap leaves the side index holding the target's old
	// contents; publishing it again would bring them back. publishIndex refuses such a side
	// index, which is not newer than the target (errSearchMirrorSideStale), and the copy starts
	// again instead.
	if err := m.checkOwner(ctx); err != nil {
		return false, err
	}
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
			"component", "catalog", "index", c.TargetUID, "err", m.errorText(err))
		documents = c.Done
	}
	now := time.Now().UTC()
	startAt := c.StartAt
	next := *row
	next.ActiveIndexUID = c.TargetUID
	next.SchemaVersion = c.SchemaVersion
	next.DocumentCount = documents
	next.LastRebuildAt = &now
	next.State.Bookmark = c.Start
	next.State.BookmarkAt = &startAt
	next.State.Copy = nil
	next.State.Window = nil
	next.State.Retry = nil
	next.State.ForceCopy = false
	next.State.State = SearchMirrorFollowing
	next.State.Reason = ""
	next.State.LastSuccessAt = &now
	next.State.LastError = ""
	next.State.ErrorSince = nil
	err = pgx.BeginFunc(ctx, m.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM bloem_search_mirror_dead_letters WHERE last_failed_at < $1`, startAt); err != nil {
			return fmt.Errorf("clear the search mirror dead-letters: %w", err)
		}
		return saveSearchMirrorRow(ctx, tx, &next)
	})
	if err != nil {
		return false, searchMirrorDatabaseError(ctx, fmt.Errorf("record the published search mirror copy: %w", err))
	}
	*row = next
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
			"component", "catalog", "err", m.errorText(err))
		return
	}
	removed := 0
	for _, uid := range staleSearchMirrorIndexUIDs(uids, m.startup.MeilisearchIndex, activeUID, previousUID) {
		if err := dropSearchMirrorCopyIndex(ctx, client, uid); err != nil {
			slog.WarnContext(ctx, "search mirror: could not remove a superseded index",
				"component", "catalog", "index", uid, "removed", removed, "err", m.errorText(err))
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
