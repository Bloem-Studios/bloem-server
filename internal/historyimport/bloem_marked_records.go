package historyimport

// Bloem-owned. A bulk "mark played" is not a play. Media servers stamp every
// item a person marks played at once (a season, a whole show) with that one
// minute, so a source account with three or more different items stamped in the
// same minute marked them rather than watched them. Those records still make
// the items watched, but their history is a manual mark (the source Bloem's own
// "Mark watched" writes), not an imported play: only real plays count as plays,
// as Floppy and Pastime count them. The hooks in service.go and types.go are
// temporary seams until the Emby servers retire at the end of November 2026.
//
// Silo streams an import page by page, and a source need not list its items by
// play time (Emby lists them by name), so one minute's items can arrive pages
// apart. withBulkMarks therefore reads the source twice: a first pass only
// counts each minute's items, and the import pass marks every record of a
// minute that reached the minimum, the first ones included.

import (
	"context"
	"hash/maphash"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// bulkMarkMinimum is how many different source items stamped in one minute make
// a bulk mark. Pastime marks plays by the same rule.
const bulkMarkMinimum = 3

// markBulkMarkedRecords sets Marked on every played record whose play time
// shares its minute with at least two other source items. One run reads one
// source account, so the minutes are that person's. A multi-episode file
// expands to several records with one ExternalID and counts once.
func markBulkMarkedRecords(records []Record) {
	minutes := newBulkMarkMinutes()
	minutes.count(records)
	minutes.mark(records)
}

// maxBulkMarkMinutes bounds the minutes one run tracks, about 25 MiB when
// full. Past it a new minute is not tracked and its records stay plays.
const maxBulkMarkMinutes = 500_000

// bulkMarkMinutes counts, per minute, the different source items played in
// it, up to bulkMarkMinimum. Items are kept as hashes, so the memory a run
// holds does not grow with the length of the source's ids.
type bulkMarkMinutes struct {
	seed  maphash.Seed
	items map[int64]minuteItems
}

type minuteItems struct {
	seen [bulkMarkMinimum - 1]uint64
	n    int
}

func newBulkMarkMinutes() *bulkMarkMinutes {
	return &bulkMarkMinutes{seed: maphash.MakeSeed(), items: make(map[int64]minuteItems)}
}

func (m *bulkMarkMinutes) count(records []Record) {
	for _, record := range records {
		minute, ok := playedMinute(record)
		if !ok {
			continue
		}
		entry, tracked := m.items[minute]
		if !tracked && len(m.items) >= maxBulkMarkMinutes {
			continue
		}
		if entry.n >= bulkMarkMinimum {
			continue
		}
		id := maphash.String(m.seed, record.ExternalID)
		known := false
		for _, seen := range entry.seen[:entry.n] {
			known = known || seen == id
		}
		if known {
			continue
		}
		if entry.n < len(entry.seen) {
			entry.seen[entry.n] = id
		}
		entry.n++
		m.items[minute] = entry
	}
}

func (m *bulkMarkMinutes) mark(records []Record) {
	for i := range records {
		minute, ok := playedMinute(records[i])
		if ok && m.items[minute].n >= bulkMarkMinimum {
			records[i].Marked = true
		}
	}
}

// playedMinute is the Unix minute a played record was stamped in.
func playedMinute(record Record) (int64, bool) {
	if !record.Played || record.LastPlayedAt == nil || record.ExternalID == "" || record.Watchlisted {
		return 0, false
	}
	return record.LastPlayedAt.UTC().Truncate(time.Minute).Unix() / 60, true
}

// withBulkMarks counts the source's minutes in a first pass, then streams the
// import with bulk-marked records marked. The first pass starts at the
// beginning even for a resumed run, so a minute counts every item the source
// lists, and gets its own run budget. The import pass counts its pages too, so
// an item the source added between the passes still counts.
func withBulkMarks(provider Provider) Provider {
	return bulkMarkProvider{Provider: provider}
}

type bulkMarkProvider struct {
	Provider
}

func (p bulkMarkProvider) Stream(ctx context.Context, from *StreamPosition, sink RecordSink) error {
	minutes := newBulkMarkMinutes()
	err := p.Provider.Stream(withRunBudget(ctx), nil, func(batch RecordBatch) error {
		minutes.count(batch.Records)
		return nil
	})
	if err != nil {
		return err
	}
	return p.Provider.Stream(ctx, from, func(batch RecordBatch) error {
		minutes.count(batch.Records)
		minutes.mark(batch.Records)
		return sink(batch)
	})
}

// importedHistorySource is the history source of an imported record: a manual
// mark for a bulk-marked record, an imported play otherwise.
func importedHistorySource(record Record) userstore.WatchHistorySource {
	if record.Marked {
		return userstore.WatchHistorySourceManual
	}
	return userstore.WatchHistorySourceImport
}
