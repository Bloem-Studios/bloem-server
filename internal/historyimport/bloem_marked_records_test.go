package historyimport

// Bloem-owned tests for bulk "mark played" records (bloem_marked_records.go).

import (
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

func markedTestRecord(id string, at time.Time) Record {
	return Record{ExternalID: id, Played: true, LastPlayedAt: &at}
}

func TestBulkMarkedRecordsAreMarksNotPlays(t *testing.T) {
	marked := time.Date(2026, 5, 16, 22, 43, 5, 0, time.UTC)
	watched := time.Date(2026, 5, 16, 21, 10, 0, 0, time.UTC)
	records := []Record{
		// Three items marked played in one minute.
		markedTestRecord("ep1", marked), markedTestRecord("ep2", marked.Add(20*time.Second)), markedTestRecord("ep3", marked.Add(50*time.Second)),
		// A double-episode file played once: two records, one source item.
		markedTestRecord("double", watched), markedTestRecord("double", watched), markedTestRecord("movie", watched.Add(10*time.Second)),
		// Watched on its own.
		markedTestRecord("film", watched.Add(time.Hour)),
		// Not played, or undated: never marks.
		{ExternalID: "resume", PositionSeconds: 60, LastPlayedAt: &marked},
		{ExternalID: "undated", Played: true},
	}
	markBulkMarkedRecords(records)
	want := []bool{true, true, true, false, false, false, false, false, false}
	for i, record := range records {
		if record.Marked != want[i] {
			t.Fatalf("record %d (%s) marked=%v, want %v", i, record.ExternalID, record.Marked, want[i])
		}
	}
	if got := importedHistorySource(records[0]); got != userstore.WatchHistorySourceManual {
		t.Fatalf("a bulk mark is recorded as %q", got)
	}
	if got := importedHistorySource(records[6]); got != userstore.WatchHistorySourceImport {
		t.Fatalf("a play is recorded as %q", got)
	}
}

// The minute is the boundary, as in Pastime: items a few seconds apart across
// two minutes do not add up.
func TestBulkMarksCountWithinOneMinute(t *testing.T) {
	edge := time.Date(2026, 4, 8, 19, 34, 59, 0, time.UTC)
	records := []Record{markedTestRecord("a", edge), markedTestRecord("b", edge), markedTestRecord("c", edge.Add(2*time.Second))}
	markBulkMarkedRecords(records)
	for _, record := range records {
		if record.Marked {
			t.Fatalf("%s marked across a minute boundary", record.ExternalID)
		}
	}
}
