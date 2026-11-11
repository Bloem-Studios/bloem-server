package catalog

// Placeholder for Task 6 of the search mirror plan, so parallel tasks compile.
// The Task 6 implementer deletes this file and writes the real code.

import (
	"context"
	"time"
)

func searchMirrorNeedsCopy(row searchMirrorRow, main SearchIndexState, now time.Time, maxLag time.Duration) (string, bool) {
	panic("search mirror: task 6 placeholder")
}

func (m *SearchMirror) startCopy(ctx context.Context, row *searchMirrorRow, main SearchIndexState, reason string) error {
	panic("search mirror: task 6 placeholder")
}

func (m *SearchMirror) continueCopy(ctx context.Context, row *searchMirrorRow, deadline time.Time) (bool, error) {
	panic("search mirror: task 6 placeholder")
}

func staleSearchMirrorIndexUIDs(uids []string, prefix, activeUID, previousUID string) []string {
	panic("search mirror: task 6 placeholder")
}
