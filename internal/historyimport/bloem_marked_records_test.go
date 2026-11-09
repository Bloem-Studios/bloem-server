package historyimport

// Bloem-owned tests for bulk "mark played" records (bloem_marked_records.go).

import (
	"context"
	"slices"
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

// bulkMarkPages delivers the records perPage at a time, in order, as a source
// whose listing is not sorted by play time spreads a bulk mark across pages.
type bulkMarkPages struct {
	records []Record
	perPage int
	starts  []*StreamPosition
}

func (p *bulkMarkPages) Stream(_ context.Context, from *StreamPosition, sink RecordSink) error {
	p.starts = append(p.starts, from)
	for i := stageOffset(from, "listing"); i < len(p.records); i += p.perPage {
		end := min(i+p.perPage, len(p.records))
		if err := sink(RecordBatch{Records: p.records[i:end], Resume: StreamPosition{Stage: "listing", Offset: i}}); err != nil {
			return err
		}
	}
	return nil
}

// Silo's streamed import reads a source once, and Silo wins, so a minute's
// items are counted as they arrive. A page is counted before it is marked; an
// item that arrived on an earlier page, before its minute reached the minimum,
// stays a play.
func TestBulkMarksReadTheSourceOnce(t *testing.T) {
	marked := time.Date(2026, 5, 16, 22, 43, 5, 0, time.UTC)
	watched := time.Date(2026, 5, 16, 21, 10, 0, 0, time.UTC)
	records := []Record{
		markedTestRecord("ep1", marked),
		markedTestRecord("film", watched),
		markedTestRecord("ep2", marked.Add(20*time.Second)),
		markedTestRecord("double", watched.Add(time.Hour)),
		markedTestRecord("double", watched.Add(time.Hour)),
		markedTestRecord("ep3", marked.Add(50*time.Second)),
		markedTestRecord("ep4", marked.Add(54*time.Second)),
	}
	for _, tc := range []struct {
		name    string
		perPage int
		from    *StreamPosition
		want    map[string]bool
	}{
		{"one page", len(records), nil, map[string]bool{"ep1": true, "film": false, "ep2": true, "double": false, "ep3": true, "ep4": true}},
		{"one record per page", 1, nil, map[string]bool{"ep1": false, "film": false, "ep2": false, "double": false, "ep3": true, "ep4": true}},
		{"resumed run", 1, &StreamPosition{Stage: "listing", Offset: 2}, map[string]bool{"ep2": false, "double": false, "ep3": false, "ep4": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &bulkMarkPages{records: slices.Clone(records), perPage: tc.perPage}
			got := map[string]bool{}
			err := withBulkMarks(source).Stream(context.Background(), tc.from, func(batch RecordBatch) error {
				for _, record := range batch.Records {
					got[record.ExternalID] = record.Marked
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("imported %v, want %v", got, tc.want)
			}
			for id, want := range tc.want {
				if got[id] != want {
					t.Fatalf("%s marked=%v, want %v (all: %v)", id, got[id], want, got)
				}
			}
			if len(source.starts) != 1 || source.starts[0] != tc.from {
				t.Fatalf("source read from %v, want once from %v", source.starts, tc.from)
			}
		})
	}
}
