package catalog

// Placeholder for Task 5 of the search mirror plan, so parallel tasks compile.
// The Task 5 implementer deletes this file and writes the real code.

import (
	"context"
	"time"
)

type searchMirrorEvent struct {
	TxID              uint64
	ID                int64
	Action            string
	ContentID         string
	PreviousContentID string
}

func takeSearchMirrorSnapshot(ctx context.Context, q pgxQuerier) (string, time.Time, error) {
	panic("search mirror: task 5 placeholder")
}

func searchMirrorWindowPage(ctx context.Context, q pgxQuerier, bookmark, upto string, afterTx uint64, afterID int64, limit int) ([]searchMirrorEvent, error) {
	panic("search mirror: task 5 placeholder")
}

func searchMirrorBookmarkAhead(ctx context.Context, q pgxQuerier, bookmark string) (bool, error) {
	panic("search mirror: task 5 placeholder")
}

func searchMirrorItemIDs(events []searchMirrorEvent) []string {
	panic("search mirror: task 5 placeholder")
}

func (m *SearchMirror) applyItems(ctx context.Context, client *meilisearchClient, indexUID string, ids []string) error {
	panic("search mirror: task 5 placeholder")
}

func (m *SearchMirror) follow(ctx context.Context, row *searchMirrorRow, deadline time.Time) error {
	panic("search mirror: task 5 placeholder")
}
