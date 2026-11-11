package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// Following (§3, §4): the mirror reads the outbox by transaction snapshot, not by row number.
// A window is the events of the transactions that finished between the bookmark and a newer
// snapshot (Window.Upto). It is read in (tx_id, id) order, a page at a time, and the resume point
// (Window.AfterTx, Window.AfterID) is saved after every page, so a restart continues exactly
// where it stopped. The bookmark moves to Window.Upto only once the window is drained.
//
// Every event is read as "item X changed": the mirror rebuilds the item's document from Postgres
// and upserts it, or deletes it when the item no longer exists. The event's action is never
// trusted, so order and repeats do not matter.

// searchMirrorEvent is one outbox row inside a window.
type searchMirrorEvent struct {
	TxID              uint64
	ID                int64
	Action            string
	ContentID         string
	PreviousContentID string
}

// searchMirrorApplyError is an error tagged with the kind of trouble it is, which decides what
// the mirror does next (see searchMirrorErrorKind). applyItems returns only these. Unwrap keeps
// the underlying Meilisearch or Postgres error reachable.
type searchMirrorApplyError struct {
	kind searchMirrorErrorKind
	err  error
}

func (e *searchMirrorApplyError) Error() string { return e.err.Error() }

func (e *searchMirrorApplyError) Unwrap() error { return e.err }

// Kind is the kind the error was tagged with. Read it with
// errors.AsType[interface{ Kind() searchMirrorErrorKind }]: classifySearchMirrorError alone
// cannot tell a Postgres error (database) from a Meilisearch rejection.
func (e *searchMirrorApplyError) Kind() searchMirrorErrorKind { return e.kind }

// searchMirrorMeilisearchError tags an error from a mirror call with its classified kind.
func searchMirrorMeilisearchError(err error) error {
	if err == nil {
		return nil
	}
	return &searchMirrorApplyError{kind: classifySearchMirrorError(err), err: err}
}

// searchMirrorDatabaseError tags a Postgres error as database. A canceled context is tagged
// unreachable instead, the kind that spends no try, so a shutdown never counts against a page.
func searchMirrorDatabaseError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	kind := searchMirrorErrDatabase
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		kind = searchMirrorErrUnreachable
	}
	return &searchMirrorApplyError{kind: kind, err: err}
}

// takeSearchMirrorSnapshot returns the database's current transaction snapshot and the database
// time it was taken.
func takeSearchMirrorSnapshot(ctx context.Context, q pgxQuerier) (string, time.Time, error) {
	var snapshot string
	var at time.Time
	if err := q.QueryRow(ctx, `SELECT pg_current_snapshot()::text, now()`).Scan(&snapshot, &at); err != nil {
		return "", time.Time{}, fmt.Errorf("take search mirror snapshot: %w", err)
	}
	return snapshot, at.UTC(), nil
}

// searchMirrorWindowPage returns up to limit events of the transactions that finished after
// bookmark and by upto, after the resume point (afterTx, afterID), in (tx_id, id) order. Rows
// with a NULL tx_id (written before the mirror existed) never match.
func searchMirrorWindowPage(ctx context.Context, q pgxQuerier, bookmark, upto string, afterTx uint64, afterID int64, limit int) ([]searchMirrorEvent, error) {
	rows, err := q.Query(ctx, `
		SELECT tx_id::text, id, action, content_id, previous_content_id
		FROM catalog_search_index_events
		WHERE provider = 'meilisearch'
		  AND tx_id >= pg_snapshot_xmin($1::pg_snapshot)
		  AND tx_id <  pg_snapshot_xmax($2::pg_snapshot)
		  AND pg_visible_in_snapshot(tx_id, $2::pg_snapshot)
		  AND NOT pg_visible_in_snapshot(tx_id, $1::pg_snapshot)
		  AND (tx_id, id) > ($3::text::xid8, $4)
		ORDER BY tx_id, id
		LIMIT $5`,
		bookmark, upto, strconv.FormatUint(afterTx, 10), afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("read search mirror window: %w", err)
	}
	defer rows.Close()
	events := make([]searchMirrorEvent, 0, limit)
	for rows.Next() {
		var event searchMirrorEvent
		var txID string
		if err := rows.Scan(&txID, &event.ID, &event.Action, &event.ContentID, &event.PreviousContentID); err != nil {
			return nil, fmt.Errorf("read search mirror window: %w", err)
		}
		if event.TxID, err = strconv.ParseUint(txID, 10, 64); err != nil {
			return nil, fmt.Errorf("read search mirror window: transaction id %q: %w", txID, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read search mirror window: %w", err)
	}
	return events, nil
}

// searchMirrorBookmarkAhead reports whether bookmark is ahead of the database's transaction
// counter. A bookmark that replicated can never be; one that is came from another database (a
// dump restored into a new cluster), so the mirror needs a full copy.
func searchMirrorBookmarkAhead(ctx context.Context, q pgxQuerier, bookmark string) (bool, error) {
	var ahead bool
	err := q.QueryRow(ctx,
		`SELECT pg_snapshot_xmax($1::pg_snapshot) > pg_snapshot_xmax(pg_current_snapshot())`,
		bookmark).Scan(&ahead)
	if err != nil {
		return false, fmt.Errorf("check search mirror bookmark: %w", err)
	}
	return ahead, nil
}

// searchMirrorItemIDs is every item the events touch: each content id and each non-empty
// previous content id (a rename touches both), de-duplicated and sorted.
func searchMirrorItemIDs(events []searchMirrorEvent) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		if event.ContentID != "" {
			ids = append(ids, event.ContentID)
		}
		if event.PreviousContentID != "" {
			ids = append(ids, event.PreviousContentID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// applyItems makes each of ids on the mirror's index indexUID match the database. It rebuilds
// the documents with the main index's own builder, for what this process builds its main index
// for; deletes the ids that no longer have a document; adds the rest in payload batches, so a
// page too big for one request is never mistaken for a rejected one; and then clears the ids'
// dead-letters, since any later change to an item supersedes its failure. Every error it returns
// is a *searchMirrorApplyError.
func (m *SearchMirror) applyItems(ctx context.Context, client *meilisearchClient, indexUID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	docs, err := m.docs.LoadDocumentsByIDs(ctx, ids, m.startup.IndexTypes, m.startup.Embedder,
		m.startup.SemanticEnabled, m.startup.BinaryQuantized)
	if err != nil {
		return searchMirrorDatabaseError(ctx, fmt.Errorf("build search mirror documents: %w", err))
	}

	found := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		found[doc.ContentID] = struct{}{}
	}
	var gone []string
	for _, id := range ids {
		if _, ok := found[id]; !ok {
			gone = append(gone, id)
		}
	}
	if len(gone) > 0 {
		ref, err := client.DeleteDocuments(ctx, indexUID, gone)
		if err == nil {
			err = client.WaitTask(ctx, ref)
		}
		if err != nil {
			return searchMirrorMeilisearchError(fmt.Errorf("delete %d search mirror documents from %s: %w", len(gone), indexUID, err))
		}
	}

	for _, batch := range catalogSearchDocumentPayloadBatches(docs, m.opts.MaxPayloadBytes) {
		ref, err := client.AddDocuments(ctx, indexUID, batch)
		if err == nil {
			err = client.WaitTask(ctx, ref)
		}
		if err != nil {
			return searchMirrorMeilisearchError(fmt.Errorf("add %d search mirror documents to %s: %w", len(batch), indexUID, err))
		}
	}

	if _, err := m.pool.Exec(ctx, `DELETE FROM bloem_search_mirror_dead_letters WHERE content_id = ANY($1)`, ids); err != nil {
		return searchMirrorDatabaseError(ctx, fmt.Errorf("clear search mirror dead-letters: %w", err))
	}
	return nil
}

// follow applies the outbox to the mirror's published index, window by window, until a fresh
// window is empty or deadline passes. It saves the row after every page, so the resume point
// survives a restart, and moves the bookmark only when a window is drained.
//
// A failed page goes to pageFailed, which records the failure; follow then either moves past
// the page (it was split and its refused items dead-lettered) or stops. While a retry is
// waiting, follow applies nothing. A shutdown returns the context's error and records nothing.
func (m *SearchMirror) follow(ctx context.Context, row *searchMirrorRow, deadline time.Time) error {
	settings, err := LoadCatalogSearchSettings(ctx, m.settings)
	if err != nil {
		return searchMirrorDatabaseError(ctx, fmt.Errorf("search mirror: load search settings: %w", err))
	}
	limit := settings.SyncBatchSize
	if limit <= 0 {
		limit = DefaultMeilisearchSyncBatchSize
	}
	state := &row.State
	// RunOnce follows only while the main index is current and no copy is due, so a mirror
	// that was waiting for the main index is following now. Unreachable and failing stay until
	// a page succeeds.
	if state.State == "" || state.State == SearchMirrorWaiting {
		state.State = SearchMirrorFollowing
		state.Reason = ""
	}
	if usable, err := m.followBookmarkUsable(ctx, row); !usable || err != nil {
		return err
	}

	// fresh is true while the current window was started by this call and nothing in it has
	// been applied yet. A fresh window that is empty means the mirror has caught up.
	fresh := false
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		if state.Retry != nil && time.Now().Before(state.Retry.NextAt) {
			return nil
		}
		if state.Window == nil {
			upto, uptoAt, err := takeSearchMirrorSnapshot(ctx, m.pool)
			if err != nil {
				return searchMirrorDatabaseError(ctx, err)
			}
			state.Window = &searchMirrorWindow{Upto: upto, UptoAt: uptoAt}
			fresh = true
		}
		more, err := m.followPage(ctx, row, limit, fresh)
		if err != nil || !more {
			return err
		}
		fresh = false
	}
}

// followBookmarkUsable reports whether the mirror can follow from its bookmark. A following
// mirror always has one, set by the copy that started it; a bookmark ahead of the database's
// transaction counter came from another database. Either way the mirror needs a full copy,
// which is recorded and saved.
func (m *SearchMirror) followBookmarkUsable(ctx context.Context, row *searchMirrorRow) (bool, error) {
	state := &row.State
	reason := ""
	if state.Bookmark == "" {
		reason = "no_bookmark"
	} else {
		ahead, err := searchMirrorBookmarkAhead(ctx, m.pool, state.Bookmark)
		if err != nil {
			return false, searchMirrorDatabaseError(ctx, err)
		}
		if ahead {
			reason = "bookmark_ahead"
		}
	}
	if reason == "" {
		return true, nil
	}
	state.State = SearchMirrorNeedsFullCopy
	state.Reason = reason
	return false, m.followSave(ctx, row)
}

// followPage applies the window's next page and saves the row. more is false when follow should
// stop: a fresh window was empty (the mirror has caught up), or a failed page holds the cursor.
func (m *SearchMirror) followPage(ctx context.Context, row *searchMirrorRow, limit int, fresh bool) (more bool, err error) {
	state := &row.State
	window := state.Window
	events, err := searchMirrorWindowPage(ctx, m.pool, state.Bookmark, window.Upto, window.AfterTx, window.AfterID, limit)
	if err != nil {
		return false, searchMirrorDatabaseError(ctx, err)
	}
	if len(events) == 0 {
		// The window is drained: every transaction up to Upto is on the mirror.
		bookmarkAt := window.UptoAt
		state.Bookmark, state.BookmarkAt = window.Upto, &bookmarkAt
		state.Window = nil
		return !fresh, m.followSave(ctx, row)
	}

	last := events[len(events)-1]
	ids := searchMirrorItemIDs(events)
	if applyErr := m.applyItems(ctx, m.mirror, row.ActiveIndexUID, ids); applyErr != nil {
		return m.followPageFailed(ctx, row, ids, last, applyErr)
	}

	now := time.Now().UTC()
	window.AfterTx, window.AfterID = last.TxID, last.ID
	state.LastSuccessAt = &now
	row.LastSyncAt = &now
	if state.State == SearchMirrorUnreachable || state.State == SearchMirrorFailing {
		state.State = SearchMirrorFollowing
		state.LastError = ""
		state.ErrorSince = nil
	}
	state.Retry = nil
	return true, m.followSave(ctx, row)
}

// followPageFailed hands a failed page to pageFailed. When pageFailed says to move on (the page
// was split and its refused items dead-lettered), the cursor moves past the page's last row.
// A shutdown records nothing.
func (m *SearchMirror) followPageFailed(ctx context.Context, row *searchMirrorRow, ids []string, last searchMirrorEvent, applyErr error) (more bool, err error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	advance, err := m.pageFailed(ctx, row, ids, applyErr)
	if err != nil || !advance || row.State.Window == nil {
		return false, err
	}
	row.State.Window.AfterTx, row.State.Window.AfterID = last.TxID, last.ID
	return true, m.followSave(ctx, row)
}

// followSave saves the mirror's row, tagging a failure as a database error.
func (m *SearchMirror) followSave(ctx context.Context, row *searchMirrorRow) error {
	if err := saveSearchMirrorRow(ctx, m.pool, *row); err != nil {
		return searchMirrorDatabaseError(ctx, err)
	}
	return nil
}
