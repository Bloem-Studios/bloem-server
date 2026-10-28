package libraryingest

import (
	"context"
	"log/slog"
	"time"
)

// scanPageTimesEvery is how many pages a storage scan applies between logs
// of where their time went.
const scanPageTimesEvery = 50

// scanPageTimes adds up how long a storage scan waited for its pages (listing
// and preparing them) and how long it spent applying them, so a slow scan
// shows whether it waits on its provider or on the catalog.
type scanPageTimes struct {
	pages, entries    int
	waiting, applying time.Duration
}

func (t *scanPageTimes) add(ctx context.Context, libraryID, entries int, waiting, applying time.Duration) {
	t.pages++
	t.entries += entries
	t.waiting += waiting
	t.applying += applying
	if t.pages < scanPageTimesEvery {
		return
	}
	slog.InfoContext(ctx, "storage scan pages", "component", "libraryingest", "library_id", libraryID,
		"pages", t.pages, "entries", t.entries, "waiting_ms", t.waiting.Milliseconds(), "applying_ms", t.applying.Milliseconds())
	*t = scanPageTimes{}
}
