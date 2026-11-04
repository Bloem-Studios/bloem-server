package historyimport

// Bloem-owned. A bulk "mark played" is not a play. Media servers stamp every
// item a person marks played at once (a season, a whole show) with that one
// minute, so a source account with three or more different items stamped in the
// same minute marked them rather than watched them. Those records still make
// the items watched, but their history is a manual mark (the source Bloem's own
// "Mark watched" writes), not an imported play: only real plays count as plays,
// as Floppy and Pastime count them. The hooks in service.go and types.go are
// temporary seams until the Emby servers retire at the end of November 2026.

import (
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
	items := make(map[time.Time]map[string]struct{})
	for _, record := range records {
		minute, ok := playedMinute(record)
		if !ok {
			continue
		}
		if items[minute] == nil {
			items[minute] = make(map[string]struct{})
		}
		items[minute][record.ExternalID] = struct{}{}
	}
	for i := range records {
		minute, ok := playedMinute(records[i])
		if ok && len(items[minute]) >= bulkMarkMinimum {
			records[i].Marked = true
		}
	}
}

func playedMinute(record Record) (time.Time, bool) {
	if !record.Played || record.LastPlayedAt == nil || record.ExternalID == "" || record.Watchlisted {
		return time.Time{}, false
	}
	return record.LastPlayedAt.UTC().Truncate(time.Minute), true
}

// importedHistorySource is the history source of an imported record: a manual
// mark for a bulk-marked record, an imported play otherwise.
func importedHistorySource(record Record) userstore.WatchHistorySource {
	if record.Marked {
		return userstore.WatchHistorySourceManual
	}
	return userstore.WatchHistorySourceImport
}
