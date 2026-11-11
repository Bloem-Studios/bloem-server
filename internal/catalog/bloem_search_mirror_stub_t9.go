package catalog

// Placeholder for Task 9 of the search mirror plan, so parallel tasks compile.
// The Task 9 implementer deletes this file and writes the real code.

import (
	"context"
	"time"
)

func searchMirrorBookmarkFromOldestPending(oldest uint64, hasPending bool, current pgSnapshot) pgSnapshot {
	panic("search mirror: task 9 placeholder")
}

func (m *SearchMirror) swap(ctx context.Context, row *searchMirrorRow, main SearchIndexState) error {
	panic("search mirror: task 9 placeholder")
}

func (m *SearchMirror) settle(ctx context.Context, row *searchMirrorRow) error {
	panic("search mirror: task 9 placeholder")
}

// searchMirrorSettlingOverdue: Swap.Settling and now - Swap.At > warnAfter. Read by Tasks 12 and 13
// (they pass SearchMirrorOptions{}.withDefaults().SettleWarnAfter).
func searchMirrorSettlingOverdue(s searchMirrorState, now time.Time, warnAfter time.Duration) bool {
	panic("search mirror: task 9 placeholder")
}
