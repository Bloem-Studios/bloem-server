package catalog

// Placeholder for Task 10 of the search mirror plan, so parallel tasks compile.
// The Task 10 implementer deletes this file and writes the real code.

import (
	"context"
	"time"
)

func searchMirrorRetryDelay(attempts int, base, max time.Duration) time.Duration {
	panic("search mirror: task 10 placeholder")
}

func (m *SearchMirror) unreachable(ctx context.Context, row *searchMirrorRow, err error) error {
	panic("search mirror: task 10 placeholder")
}

func (m *SearchMirror) pageFailed(ctx context.Context, row *searchMirrorRow, ids []string, err error) (bool, error) {
	panic("search mirror: task 10 placeholder")
}

func (m *SearchMirror) splitApply(ctx context.Context, client *meilisearchClient, uid string, ids []string) (accepted, refused []string, abort error) {
	panic("search mirror: task 10 placeholder")
}

func (m *SearchMirror) retryDeadLetters(ctx context.Context, row *searchMirrorRow) error {
	panic("search mirror: task 10 placeholder")
}

// handleRunError records a failure that is not a follow page (copy batches, publish, markers):
// by classifySearchMirrorError kind — unreachable -> m.unreachable; key_rejected -> misconfigured/key_rejected;
// rejected or database -> Errors[kind]++, LastError, ErrorSince (if unset), State failing; then saves.
// RunOnce (Task 8) calls it for errors from settleTargets, startCopy and continueCopy.
func (m *SearchMirror) handleRunError(ctx context.Context, row *searchMirrorRow, err error) error {
	panic("search mirror: task 10 placeholder")
}
