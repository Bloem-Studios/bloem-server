package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
	"github.com/jackc/pgx/v5"
)

// The failover step (spec §7). After a promotion the new live copy's main server is the old
// mirror, and the old main becomes the mirror. The swap step brings both up to date:
//   - the new main gets every change the old mirror had not received, by reopening the main
//     index's outbox rows its bookmark did not cover, or by a main rebuild when it held no
//     usable copy;
//   - the new mirror (the old main) gets a bookmark from the main index's oldest pending row,
//     so it receives everything the old main had not finished.
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
)

// searchMirrorBookmarkFromOldestPending is the new mirror's bookmark after a failover: the
// transactions older than the main index's oldest pending row that were not running at the
// swap. Every row of such a transaction had been processed by the old main, so it is on the
// new mirror; everything else is replayed to it. Without a pending row it is current.
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
	_, at, ok := row.State.hold()
	return ok && now.Sub(at) <= maxLag
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
func (m *SearchMirror) swap(ctx context.Context, row *searchMirrorRow, main SearchIndexState) error {
	if last := row.State.Swap; last != nil && !m.startedAt.After(last.At) && last.By != m.opts.InstanceID {
		if m.stoodDown.CompareAndSwap(false, true) {
			slog.WarnContext(ctx, "search mirror: another Silo copy took over search; this process stops mirroring",
				"component", "catalog", "swapped_at", last.At, "swapped_by", last.By)
		}
		return nil
	}

	lock, locked, err := pglock.Acquire(ctx, m.pool, m.opts.MainLockKey, searchMirrorSwapLockWait)
	if err != nil {
		return fmt.Errorf("search mirror swap: take the main search index lock: %w", err)
	}
	if !locked {
		slog.InfoContext(ctx, "search mirror: the main search index is busy; the role swap waits for the next run",
			"component", "catalog")
		return nil
	}
	defer func() {
		if err := lock.Release(ctx); err != nil {
			slog.WarnContext(ctx, "search mirror: releasing the main search index lock", "component", "catalog", "error", err)
		}
	}()
	conn := lock.Conn()

	// The main sync and rebuild cannot change the main state while the lock is held; read it
	// again, since a rebuild may have published while this step waited for the lock.
	main, err = NewSearchIndexEventRepository(m.pool).GetState(ctx, SearchProviderMeilisearch)
	if err != nil {
		return fmt.Errorf("search mirror swap: read the main search index state: %w", err)
	}
	var checkedAt time.Time
	if err := conn.QueryRow(ctx, `SELECT now()`).Scan(&checkedAt); err != nil {
		return fmt.Errorf("search mirror swap: read the database clock: %w", err)
	}
	following := searchMirrorSwapCanReopen(*row, main, checkedAt, m.swapMaxLag(*row))

	// Not following: the new main (this process's main server, the old mirror) needs the main
	// rebuild only when it holds a stale index under the current name. Without one, the main
	// sync rebuilds the missing index by itself (catalogSearchIndexRequiresRebuildOnTarget).
	startRebuild := false
	if !following && main.ActiveIndexUID != "" {
		exists, err := m.main.indexExists(ctx, main.ActiveIndexUID)
		if err != nil {
			return fmt.Errorf("search mirror swap: check %s on the new main search server: %w", main.ActiveIndexUID, err)
		}
		startRebuild = exists
	}

	next := *row
	var reopened, deadLetters int64
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		// Both before anything is reopened, in one statement so they see the same commits.
		var oldestText *string
		var currentText string
		var now time.Time
		if err := tx.QueryRow(ctx, `
			SELECT (
				SELECT min(tx_id)::text FROM catalog_search_index_events
				WHERE provider = $1 AND processed_at IS NULL
			), pg_current_snapshot()::text, now()`, SearchProviderMeilisearch).
			Scan(&oldestText, &currentText, &now); err != nil {
			return fmt.Errorf("read the oldest pending search event: %w", err)
		}
		current, err := parsePgSnapshot(currentText)
		if err != nil {
			return err
		}
		var oldest uint64
		if oldestText != nil {
			if oldest, err = strconv.ParseUint(*oldestText, 10, 64); err != nil {
				return fmt.Errorf("read the oldest pending search event: %w", err)
			}
		}

		if following {
			hold, _, _ := row.State.hold()
			tag, err := tx.Exec(ctx, `
				UPDATE catalog_search_index_events
				SET processed_at = NULL, attempts = 0, available_at = now(), last_error = ''
				WHERE provider = $1
				  AND processed_at IS NOT NULL
				  AND tx_id IS NOT NULL
				  AND NOT pg_visible_in_snapshot(tx_id, $2::text::pg_snapshot)`,
				SearchProviderMeilisearch, hold)
			if err != nil {
				return fmt.Errorf("reopen the search events the old mirror lacked: %w", err)
			}
			reopened = tag.RowsAffected()
			tag, err = tx.Exec(ctx, `
				INSERT INTO catalog_search_index_events (provider, action, content_id)
				SELECT $1, $2, content_id FROM bloem_search_mirror_dead_letters`,
				SearchProviderMeilisearch, SearchIndexEventUpsert)
			if err != nil {
				return fmt.Errorf("queue the old mirror's dead-lettered items: %w", err)
			}
			deadLetters = tag.RowsAffected()
			if _, err := tx.Exec(ctx, `DELETE FROM bloem_search_mirror_dead_letters`); err != nil {
				return fmt.Errorf("clear the old mirror's dead letters: %w", err)
			}
		}

		// The new mirror is the old main: it holds the main index's current index, up to the
		// oldest row the old main had not finished.
		now = now.UTC()
		state := &next.State
		state.Bookmark = searchMirrorBookmarkFromOldestPending(oldest, oldestText != nil, current).String()
		state.BookmarkAt = &now
		next.ActiveIndexUID = main.ActiveIndexUID
		next.SchemaVersion = main.SchemaVersion
		next.DocumentCount = main.DocumentCount
		next.LastRebuildAt = utcTimePtr(main.LastRebuildAt)
		state.Copy = nil
		state.Window = nil
		// Progress and trouble recorded for the old mirror server do not describe the new one.
		state.Retry = nil
		state.ForceCopy = false
		state.State = SearchMirrorFollowing
		state.Reason = ""
		state.LastError = ""
		state.ErrorSince = nil

		state.MainTarget, state.MirrorTarget = state.MirrorTarget, state.MainTarget
		state.Swap = &searchMirrorSwap{At: now, By: m.opts.InstanceID, Settling: true}
		state.RoleSwaps++
		return saveSearchMirrorRow(ctx, tx, next)
	})
	if err != nil {
		return fmt.Errorf("search mirror swap: %w", err)
	}
	*row = next
	slog.InfoContext(ctx, "search mirror: roles swapped after a failover",
		"component", "catalog", "index", main.ActiveIndexUID, "replayed_by_reopen", following,
		"reopened_events", reopened, "requeued_dead_letters", deadLetters, "main_rebuild", startRebuild)

	if startRebuild {
		if m.opts.StartTask == nil {
			slog.ErrorContext(ctx, "search mirror: the new main search server holds a stale index; start rebuild_catalog_search_index by hand",
				"component", "catalog", "index", main.ActiveIndexUID)
			return nil
		}
		if err := m.opts.StartTask(searchMirrorSwapRebuildTask); err != nil {
			slog.ErrorContext(ctx, "search mirror: could not start the main search rebuild after a failover; start rebuild_catalog_search_index by hand",
				"component", "catalog", "index", main.ActiveIndexUID, "error", err)
			return fmt.Errorf("search mirror swap: start %s: %w", searchMirrorSwapRebuildTask, err)
		}
	}
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
		return err
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
		return saveSearchMirrorRow(ctx, tx, next)
	})
	if err != nil {
		return fmt.Errorf("search mirror settle: %w", err)
	}
	*row = next
	slog.InfoContext(ctx, "search mirror: failover settled; the main index re-sends what was processed since the swap",
		"component", "catalog", "swapped_at", swap.At, "reopened_events", reopened)
	return nil
}
