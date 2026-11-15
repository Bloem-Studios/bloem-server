package scanner

import (
	"testing"
	"time"
)

// TestClassifyEbookSkipReprocessesAnUnlinkedFile keeps Bloem's skip
// classifier in step with Silo's unchangedEbookFile: an unchanged file whose
// item has no membership in the file's library is reprocessed in full, so a
// scan that stopped before writing the membership, or lost the item to a
// concurrent sweep, puts the book back in the library.
func TestClassifyEbookSkipReprocessesAnUnlinkedFile(t *testing.T) {
	modifiedAt := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	file := ebookSkipFile{
		contentID:       "book-1",
		path:            "/library/book.epub",
		size:            42,
		modifiedAt:      &modifiedAt,
		groupKeyVersion: ebookGroupKeyVersion,
		status:          "matched",
		linked:          true,
	}

	if id, unchanged, localRepair := classifyEbookSkip([]ebookSkipFile{file}, file.path, file.size, modifiedAt); id != "book-1" || !unchanged || localRepair {
		t.Fatalf("linked unchanged file = (%q, %v, %v), want (book-1, true, false)", id, unchanged, localRepair)
	}
	if _, unchanged := unchangedEbookFile([]ebookSkipFile{file}, file.path, file.size, modifiedAt); !unchanged {
		t.Fatal("Silo's unchangedEbookFile no longer skips a linked unchanged file; recheck this test")
	}

	file.linked = false
	if _, unchanged := unchangedEbookFile([]ebookSkipFile{file}, file.path, file.size, modifiedAt); unchanged {
		t.Fatal("Silo's unchangedEbookFile skips an unlinked file; recheck this test")
	}
	if id, unchanged, localRepair := classifyEbookSkip([]ebookSkipFile{file}, file.path, file.size, modifiedAt); unchanged || localRepair {
		t.Fatalf("unlinked unchanged file = (%q, %v, %v), want a full reprocess (unchanged=false, localRepair=false)", id, unchanged, localRepair)
	}

	// A pending cover retry on an unlinked file is a full reprocess too, as
	// Silo reprocesses every unlinked file: its enrichment is queued again.
	file.coverRetry = true
	if id, unchanged, localRepair := classifyEbookSkip([]ebookSkipFile{file}, file.path, file.size, modifiedAt); unchanged || localRepair {
		t.Fatalf("unlinked file with a cover retry = (%q, %v, %v), want a full reprocess", id, unchanged, localRepair)
	}
}
