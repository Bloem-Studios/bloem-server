package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
)

// The failover step (spec §7). After a promotion the new live copy's main server is the old
// mirror, and the old main becomes the mirror. The swap step brings both up to date:
//   - the new main gets every change the old mirror had not received, by reopening the main
//     index's outbox rows its bookmark did not cover, or by a main rebuild when it held no
//     usable copy;
//   - the new mirror (the old main) gets a bookmark from the main index's oldest row the old main
//     may lack: a pending row, or one the new copy's processes may have sent only to the new main
//     before the swap. It receives everything from there.
//
// Neither needs the mirror server, so the swap runs while the old main is down (a crash
// failover), from the main's marker alone.
//
// The old Silo copy may keep running its main sync for a while after the swap, sending rows to
// the old main and marking them done. The settle replay reopens every row marked done since the
// swap once no process older than the swap is alive, so the new main gets those too.

const (
	// searchMirrorSwapLockWait is how long the swap step waits for the main index's lock. When
	// the wait runs out, the swap is tried again on the next run.
	searchMirrorSwapLockWait = 2 * time.Minute
	// searchMirrorSwapRebuildTask is the main index's rebuild task, started after a failover
	// onto a server that holds a stale index under the current name.
	searchMirrorSwapRebuildTask = "rebuild_catalog_search_index"
	// searchMirrorSwapSinceMargin widens the new mirror's replay to rows processed a little
	// before the earliest new-copy process's lease began: a process's main sync may start before
	// its first lease touch.
	searchMirrorSwapSinceMargin = 5 * time.Minute
)

// searchMirrorBookmarkFromOldestPending is the new mirror's bookmark after a failover: the
// transactions older than the oldest row the old main may lack that were not running at the
// swap. Every row of such a transaction had been processed by the old main, so it is on the new
// mirror; everything else is replayed to it. Without such a row it is current.
func searchMirrorBookmarkFromOldestPending(oldest uint64, hasPending bool, current pgSnapshot) pgSnapshot {
	if !hasPending {
		return current
	}
	// oldest belongs to a committed row, so it is below current.Xmax; the min only guards
	// against a bookmark that would cover transactions that had not started.
	bookmark := pgSnapshot{Xmin: min(oldest, current.Xmin), Xmax: min(oldest, current.Xmax)}
	for _, xid := range current.Xip {
		if xid < bookmark.Xmax {
			bookmark.Xip = append(bookmark.Xip, xid)
		}
	}
	return bookmark
}

// searchMirrorSettlingOverdue reports whether a swap has been settling for longer than
// warnAfter. The mirror keeps waiting; the admin page and the state metric show the warning.
func searchMirrorSettlingOverdue(s searchMirrorState, now time.Time, warnAfter time.Duration) bool {
	return s.Swap != nil && s.Swap.Settling && now.Sub(s.Swap.At) > warnAfter
}

// searchMirrorSwapCanReopen reports whether the old mirror held the main index's current index
// exactly as its bookmark describes, so reopening the rows the bookmark did not cover brings it
// up to date: same index and schema, no copy under way or owed, and within the lag cap (past
// it, cleanup may have deleted rows the bookmark did not cover).
func searchMirrorSwapCanReopen(row searchMirrorRow, main SearchIndexState, now time.Time, maxLag time.Duration) bool {
	if main.ActiveIndexUID == "" || row.ActiveIndexUID != main.ActiveIndexUID || row.SchemaVersion != main.SchemaVersion {
		return false
	}
	if row.State.Copy != nil || row.State.ForceCopy || row.State.State == SearchMirrorNeedsFullCopy {
		return false
	}
	_, since, ok := row.holdSince()
	return ok && now.Sub(since) <= maxLag
}

// searchMirrorSwapNeedsRebuild reports whether the new main (the old mirror) needs the main
// rebuild after a swap that cannot reopen rows: only when it holds a stale index under the
// main's current name. Silo's rebuild publishes every index under a fresh
// "<prefix>_rebuild_<unix seconds>" name, so an index under the main's name that the old mirror
// itself published (row.ActiveIndexUID) is the mirror's copy, which is stale; an index under a
// name the mirror never published was built on that server by a main rebuild, after the
// failover, and is fresh. Without the index the main sync rebuilds by itself
// (catalogSearchIndexRequiresRebuildOnTarget).
func searchMirrorSwapNeedsRebuild(row searchMirrorRow, main SearchIndexState, existsOnNewMain bool) bool {
	return existsOnNewMain && main.ActiveIndexUID != "" && row.ActiveIndexUID == main.ActiveIndexUID
}

// swapMaxLag is the cap the row records (the effective one, which cleanup reads too),
// or the configured one before the row has any.
func (m *SearchMirror) swapMaxLag(row searchMirrorRow) time.Duration {
	if row.State.MaxLagSeconds > 0 {
		return time.Duration(row.State.MaxLagSeconds) * time.Second
	}
	if m.cfg.MaxLag > 0 {
		return m.cfg.MaxLag
	}
	return DefaultSearchMirrorMaxLag
}

// swap runs the swap step for a "swapped" verdict: this process's main server carries the id
// remembered for the mirror.
//
// Only a process that started after the last recorded swap, or the process that recorded it,
// may swap. Any other process is the old Silo copy still running after a move: it stands down
// for the rest of its life, so it never flips the roles back.
//
// The step holds the main index's lock, so it cannot race the main sync or rebuild, and does
// all its database work in one transaction: a crash leaves the old or the new roles, never half.
// A main rebuild the new main needs is recorded in the row (RebuildMain) and started once the
// lock is released, and again on later runs until the main names another index.
func (m *SearchMirror) swap(ctx context.Context, row *searchMirrorRow) error {
	if last := row.State.Swap; last != nil && !m.startedAt.After(last.At) && last.By != m.opts.InstanceID {
		if m.stoodDown.CompareAndSwap(false, true) {
			slog.WarnContext(ctx, "search mirror: another Silo copy took over search; this process stops mirroring",
				"component", "catalog", "swapped_at", last.At, "swapped_by", last.By)
		}
		return nil
	}
	swapped, err := m.swapLocked(ctx, row)
	if err != nil || !swapped || row.State.RebuildMain == "" {
		return err
	}
	if m.opts.StartTask == nil {
		slog.ErrorContext(ctx, "search mirror: the new main search server holds a stale index; start rebuild_catalog_search_index by hand",
			"component", "catalog", "index", row.State.RebuildMain)
		return nil
	}
	main, err := NewSearchIndexEventRepository(m.pool).GetState(ctx, SearchProviderMeilisearch)
	if err != nil {
		return fmt.Errorf("search mirror swap: read the main search index state: %w", err)
	}
	return m.startOwedMainRebuild(ctx, row, main)
}

// swapLocked takes the main index's lock, decides how the new main catches up, and commits the
// swap. swapped is false when the lock was busy: the swap waits for the next run.
func (m *SearchMirror) swapLocked(ctx context.Context, row *searchMirrorRow) (swapped bool, err error) {
	lock, locked, err := pglock.Acquire(ctx, m.pool, m.opts.MainLockKey, searchMirrorSwapLockWait)
	if err != nil {
		return false, fmt.Errorf("search mirror swap: take the main search index lock: %w", err)
	}
	if !locked {
		slog.InfoContext(ctx, "search mirror: the main search index is busy; the role swap waits for the next run",
			"component", "catalog")
		return false, nil
	}
	defer func() {
		if err := lock.Release(ctx); err != nil {
			slog.WarnContext(ctx, "search mirror: releasing the main search index lock", "component", "catalog", "error", err)
		}
	}()
	conn := lock.Conn()

	// The main sync and rebuild cannot change the main state while the lock is held; read it
	// again, since a rebuild may have published while this step waited for the lock.
	main, err := NewSearchIndexEventRepository(m.pool).GetState(ctx, SearchProviderMeilisearch)
	if err != nil {
		return false, fmt.Errorf("search mirror swap: read the main search index state: %w", err)
	}
	var checkedAt time.Time
	if err := conn.QueryRow(ctx, `SELECT now()`).Scan(&checkedAt); err != nil {
		return false, fmt.Errorf("search mirror swap: read the database clock: %w", err)
	}
	maxLag := m.swapMaxLag(*row)
	plan := searchMirrorSwapPlan{main: main, maxLag: maxLag, following: searchMirrorSwapCanReopen(*row, main, checkedAt, maxLag)}
	if !plan.following && main.ActiveIndexUID != "" {
		exists, err := m.main.indexExists(ctx, main.ActiveIndexUID)
		if err != nil {
			return false, fmt.Errorf("search mirror swap: check %s on the new main search server: %w", main.ActiveIndexUID, err)
		}
		plan.rebuild = searchMirrorSwapNeedsRebuild(*row, main, exists)
	}

	next := *row
	var done searchMirrorSwapDone
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var txErr error
		done, txErr = m.swapTx(ctx, tx, &next, plan)
		return txErr
	})
	if err != nil {
		return false, fmt.Errorf("search mirror swap: %w", err)
	}
	*row = next
	slog.InfoContext(ctx, "search mirror: roles swapped after a failover",
		"component", "catalog", "index", main.ActiveIndexUID, "replayed_by_reopen", plan.following,
		"reopened_events", done.reopened, "requeued_dead_letters", done.deadLetters, "main_rebuild", plan.rebuild)
	return true, nil
}

// searchMirrorSwapPlan is what the swap step decided before its transaction.
type searchMirrorSwapPlan struct {
	main      SearchIndexState
	maxLag    time.Duration
	following bool // the new main catches up by reopening the rows the old mirror lacked
	rebuild   bool // the new main holds a stale index under the main's name: rebuild it
}

// searchMirrorSwapDone is what the swap's transaction changed, for the log.
type searchMirrorSwapDone struct {
	reopened, deadLetters int64
}

// swapTx is the swap's transaction: it reopens what the new main lacks (when following) and
// gives row the new roles, the new mirror's bookmark and the swap record, and saves it.
func (m *SearchMirror) swapTx(ctx context.Context, tx pgx.Tx, row *searchMirrorRow, plan searchMirrorSwapPlan) (searchMirrorSwapDone, error) {
	var done searchMirrorSwapDone
	// Before anything is reopened, so they see the same commits.
	bookmark, now, err := m.swapNewMirrorBookmark(ctx, tx, row.State.Swap, plan.maxLag)
	if err != nil {
		return done, err
	}
	if plan.following {
		if done, err = reopenSearchMirrorSwapRows(ctx, tx, row.State); err != nil {
			return done, err
		}
	}

	// The new mirror is the old main: it holds the main index's current index, up to the
	// oldest row the old main may lack.
	main := plan.main
	state := &row.State
	state.Bookmark = bookmark.String()
	state.BookmarkAt = &now
	row.ActiveIndexUID = main.ActiveIndexUID
	row.SchemaVersion = main.SchemaVersion
	row.DocumentCount = main.DocumentCount
	row.LastRebuildAt = utcTimePtr(main.LastRebuildAt)
	state.Copy = nil
	state.Window = nil
	// Progress and trouble recorded for the old mirror server do not describe the new one.
	state.Retry = nil
	state.ForceCopy = false
	state.State = SearchMirrorFollowing
	state.Reason = ""
	state.LastError = ""
	state.ErrorSince = nil
	state.RebuildMain = ""
	if plan.rebuild {
		state.RebuildMain = main.ActiveIndexUID
	}

	state.MainTarget, state.MirrorTarget = state.MirrorTarget, state.MainTarget
	state.Swap = &searchMirrorSwap{At: now, By: m.opts.InstanceID, Settling: true}
	state.RoleSwaps++
	return done, saveSearchMirrorRow(ctx, tx, row)
}

// swapNewMirrorBookmark computes the new mirror's bookmark, with the database clock. The oldest
// row the old main may lack is the oldest pending one, or the oldest processed since the
// earliest lease start of the processes that began after the previous swap (less
// searchMirrorSwapSinceMargin): the new copy's processes are among them, and their main sync
// may have sent rows only to the new main before this swap. Old-copy processes that began then
// widen the replay, which is harmless: every item is rebuilt from Postgres. Leases silent for
// longer than the cap are left out.
func (m *SearchMirror) swapNewMirrorBookmark(ctx context.Context, tx pgx.Tx, previous *searchMirrorSwap, maxLag time.Duration) (pgSnapshot, time.Time, error) {
	var after *time.Time
	if previous != nil {
		after = &previous.At
	}
	var oldestText *string
	var currentText string
	var now time.Time
	err := tx.QueryRow(ctx, `
		WITH since AS (
			SELECT min(started_at) - $4::bigint * interval '1 second' AS at
			FROM bloem_search_mirror_processes
			WHERE started_at > COALESCE($2::timestamptz, '-infinity'::timestamptz)
			  AND heartbeat_at > now() - $3::bigint * interval '1 second'
		)
		SELECT (
			SELECT min(tx_id)::text FROM catalog_search_index_events
			WHERE provider = $1
			  AND (processed_at IS NULL OR processed_at >= (SELECT at FROM since))
		), pg_current_snapshot()::text, now()`,
		SearchProviderMeilisearch, after, int64(maxLag/time.Second), int64(searchMirrorSwapSinceMargin/time.Second)).
		Scan(&oldestText, &currentText, &now)
	if err != nil {
		return pgSnapshot{}, time.Time{}, fmt.Errorf("read the oldest search event the old main may lack: %w", err)
	}
	current, err := parsePgSnapshot(currentText)
	if err != nil {
		return pgSnapshot{}, time.Time{}, err
	}
	var oldest uint64
	if oldestText != nil {
		if oldest, err = strconv.ParseUint(*oldestText, 10, 64); err != nil {
			return pgSnapshot{}, time.Time{}, fmt.Errorf("read the oldest search event the old main may lack: %w", err)
		}
	}
	return searchMirrorBookmarkFromOldestPending(oldest, oldestText != nil, current), now.UTC(), nil
}

// reopenSearchMirrorSwapRows reopens the main index's rows the old mirror's bookmark did not
// cover, so the main sync sends them to the new main, and queues the old mirror's dead letters
// for it.
func reopenSearchMirrorSwapRows(ctx context.Context, tx pgx.Tx, state searchMirrorState) (searchMirrorSwapDone, error) {
	var done searchMirrorSwapDone
	hold, _, _ := state.hold()
	tag, err := tx.Exec(ctx, `
		UPDATE catalog_search_index_events
		SET processed_at = NULL, attempts = 0, available_at = now(), last_error = ''
		WHERE provider = $1
		  AND processed_at IS NOT NULL
		  AND tx_id IS NOT NULL
		  AND tx_id >= pg_snapshot_xmin($2::text::pg_snapshot)
		  AND NOT pg_visible_in_snapshot(tx_id, $2::text::pg_snapshot)`,
		SearchProviderMeilisearch, hold)
	if err != nil {
		return done, fmt.Errorf("reopen the search events the old mirror lacked: %w", err)
	}
	done.reopened = tag.RowsAffected()
	tag, err = tx.Exec(ctx, `
		INSERT INTO catalog_search_index_events (provider, action, content_id)
		SELECT $1, $2, content_id FROM bloem_search_mirror_dead_letters`,
		SearchProviderMeilisearch, SearchIndexEventUpsert)
	if err != nil {
		return done, fmt.Errorf("queue the old mirror's dead-lettered items: %w", err)
	}
	done.deadLetters = tag.RowsAffected()
	if _, err := tx.Exec(ctx, `DELETE FROM bloem_search_mirror_dead_letters`); err != nil {
		return done, fmt.Errorf("clear the old mirror's dead letters: %w", err)
	}
	return done, nil
}

// startOwedMainRebuild starts the main rebuild a swap found owed (RebuildMain), outside the main
// index's lock, which the rebuild takes itself. The debt ends when the main state names another
// index, that is, once a rebuild published. Until then every run starts it again: a start that
// fails, or a rebuild that skipped because the main index was busy, is not lost. A rebuild
// already running refuses the start, which is only logged at debug level.
func (m *SearchMirror) startOwedMainRebuild(ctx context.Context, row *searchMirrorRow, main SearchIndexState) error {
	owed := row.State.RebuildMain
	if owed == "" {
		return nil
	}
	if main.ActiveIndexUID != owed {
		row.State.RebuildMain = ""
		return saveSearchMirrorRow(ctx, m.pool, row)
	}
	if m.opts.StartTask == nil { // the swap logged that it must be started by hand
		return nil
	}
	if err := m.opts.StartTask(searchMirrorSwapRebuildTask); err != nil {
		slog.DebugContext(ctx, "search mirror: the owed main search rebuild did not start; it is tried again on the next run",
			"component", "catalog", "index", owed, "error", err)
		return nil
	}
	slog.InfoContext(ctx, "search mirror: started the main search rebuild the failover left owed",
		"component", "catalog", "index", owed)
	return nil
}

// settle runs the settle replay while a swap is settling. It waits until no process that
// started before the swap, other than the one that made it, has touched its lease within
// SettleQuiet; then it reopens every main-index row marked done since the swap (less a minute's
// margin), so the main index re-sends what an old Silo copy sent to the old main, and clears
// Settling. It never reopens early, however long it waits.
func (m *SearchMirror) settle(ctx context.Context, row *searchMirrorRow) error {
	swap := row.State.Swap
	if swap == nil || !swap.Settling {
		return nil
	}
	alive, err := searchMirrorOlderProcessAlive(ctx, m.pool, swap.At, swap.By, m.opts.SettleQuiet)
	if err != nil {
		return fmt.Errorf("search mirror settle: %w", err)
	}
	if alive {
		return nil
	}

	next := *row
	settled := *swap
	settled.Settling = false
	next.State.Swap = &settled
	var reopened int64
	err = pgx.BeginFunc(ctx, m.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE catalog_search_index_events
			SET processed_at = NULL, attempts = 0, available_at = now(), last_error = ''
			WHERE provider = $1
			  AND processed_at >= $2::timestamptz - interval '1 minute'`,
			SearchProviderMeilisearch, swap.At)
		if err != nil {
			return fmt.Errorf("reopen the search events processed since the swap: %w", err)
		}
		reopened = tag.RowsAffected()
		return saveSearchMirrorRow(ctx, tx, &next)
	})
	if err != nil {
		return fmt.Errorf("search mirror settle: %w", err)
	}
	*row = next
	slog.InfoContext(ctx, "search mirror: failover settled; the main index re-sends what was processed since the swap",
		"component", "catalog", "swapped_at", swap.At, "reopened_events", reopened)
	return nil
}
