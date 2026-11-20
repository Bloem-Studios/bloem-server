package scanner

import (
	"slices"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// A storage book whose file packs every subject into one entry, with a
// BISAC code among them and an impossible publication date, is stored with
// separate genres, no code and no date.
func TestStorageEbookSplitsSubjectsAndDropsImpossibleDates(t *testing.T) {
	book, ok := StorageEbookFromEntry(storageEbookEntry("light", "epub", &storagev1.EbookMetadata{
		Title:         "The Sound of Light",
		Genres:        []string{"Historical Romance;Historical fiction;Christian fiction;Novels;FIC042030", "novels", " FIC000000 "},
		PublishedDate: "2922-11-21",
	}, "r1"))
	if !ok {
		t.Fatal("entry carries no metadata")
	}
	want := []string{"Historical Romance", "Historical fiction", "Christian fiction", "Novels"}
	if !slices.Equal(book.Book.Genres, want) {
		t.Errorf("genres = %q, want %q", book.Book.Genres, want)
	}
	if book.Book.Year != 0 || !book.Book.PublishedAt.IsZero() {
		t.Errorf("year = %d, published = %v, want neither", book.Book.Year, book.Book.PublishedAt)
	}
}

// A book announced for next year keeps its date: publishers list books ahead
// of release.
func TestStorageEbookKeepsNextYearsDate(t *testing.T) {
	next := time.Now().Year() + 1
	book := parsedEbook{PublishedAt: time.Date(next, 3, 1, 0, 0, 0, 0, time.UTC), Year: next, Genres: []string{"Fantasy"}}
	normalizeStorageEbook(&book, time.Now())
	if book.Year != next || book.PublishedAt.IsZero() || !slices.Equal(book.Genres, []string{"Fantasy"}) {
		t.Errorf("book = %+v, want next year's date and its genre kept", book)
	}
}
