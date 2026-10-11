package scanner

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

func storageFileEntry(logical string) *storagev1.Entry {
	name := logical
	for i := len(logical) - 1; i >= 0; i-- {
		if logical[i] == '/' {
			name = logical[i+1:]
			break
		}
	}
	return &storagev1.Entry{Id: logical, Name: name, LogicalPath: logical, Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Revision: "r1"}
}

func storageDirectoryEntry(logical string) *storagev1.Entry {
	entry := storageFileEntry(logical)
	entry.Kind = storagev1.EntryKind_ENTRY_KIND_DIRECTORY
	return entry
}

// Folder names from the live movies bucket. Parsing only the entry's file
// name made "13 Eerie (2013)/xyz.mkv" a movie titled "xyz" with no year, and
// discarded the folder's provider IDs.
func TestStorageMovieIdentityUsesLogicalPathFolders(t *testing.T) {
	for _, tc := range []struct {
		logical, title string
		year           int
		tmdb, imdb     string
	}{
		{"3 A.M. (2001) {imdb-tt0248661} {tmdb-77084}/3 A.M. (2001) {imdb-tt0248661} {tmdb-77084}.mkv", "3 A.M.", 2001, "77084", "tt0248661"},
		{"13 Eerie (2013)/xyz.mkv", "13 Eerie", 2013, "", ""},
		{"1080p/13 Eerie (2013)/xyz.mkv", "13 Eerie", 2013, "", ""},
		{"Alien (1979) {imdb-tt0078748}/Alien.1979.Remux-1080p.mkv", "Alien", 1979, "", "tt0078748"},
		// A source rooted at the title folder exposes only the file name.
		{"3 A.M. (2001) {imdb-tt0248661} {tmdb-77084}.mkv", "3 A.M.", 2001, "77084", "tt0248661"},
		{"xyz.mkv", "xyz", 0, "", ""},
		// Logical paths are provider display metadata. Dot segments resolve
		// inside the source; an escaping path falls back to the entry name.
		{"Movies//../Native.Movie.2024.mp4", "Native Movie", 2024, "", ""},
		{"../../elsewhere/Heat (1995).mkv", "Heat", 1995, "", ""},
	} {
		got := storageMovieIdentities([]*storagev1.Entry{storageFileEntry(tc.logical)})[0]
		if got.title != tc.title || got.year != tc.year || got.tmdbID != tc.tmdb || got.imdbID != tc.imdb || got.groupKey == "" || got.ambiguous {
			t.Errorf("%s: got title=%q year=%d tmdb=%q imdb=%q key=%q ambiguous=%v", tc.logical, got.title, got.year, got.tmdbID, got.imdbID, got.groupKey, got.ambiguous)
		}
	}
	// The escaping path must not resolve above the parse root.
	if got := storageMovieIdentities([]*storagev1.Entry{{Id: "x", Name: "Heat (1995).mkv", LogicalPath: "../../Heat (1995).mkv", Kind: storagev1.EntryKind_ENTRY_KIND_FILE}})[0]; got.parsePath != storageMovieParseRoot+"/Heat (1995).mkv" {
		t.Fatalf("escaping logical path parsed as %q", got.parsePath)
	}
}

// Editions of one title share Silo's content group, so they publish as
// versions of one item instead of one item per file.
func TestStorageMovieIdentityGroupsEditions(t *testing.T) {
	ids := storageMovieIdentities([]*storagev1.Entry{
		storageFileEntry("Heat (1995) {tmdb-949}/Heat (1995) - Director's Cut.mkv"),
		storageFileEntry("Heat (1995) {tmdb-949}/Heat (1995).mkv"),
		storageFileEntry("4K/Heat (1995) {tmdb-949}/Heat (1995).mkv"),
		storageFileEntry("Ronin (1998)/Ronin (1998).mkv"),
	})
	if ids[0].groupKey != ids[1].groupKey || ids[1].groupKey != ids[2].groupKey || ids[3].groupKey == ids[0].groupKey {
		t.Fatalf("group keys %q %q %q %q", ids[0].groupKey, ids[1].groupKey, ids[2].groupKey, ids[3].groupKey)
	}
	if ids[0].variant == nil || ids[0].variant.EditionKey != "director_cut" || (ids[1].variant != nil && ids[1].variant.EditionKey != "") {
		t.Fatalf("edition hints %+v %+v", ids[0].variant, ids[1].variant)
	}
}

// Silo's walk never collects samples, sidecar-only folders, local extras or
// its ignored directories. Native movie publication must not turn them into
// catalog movies either.
func TestStorageMovieFileSkippedFollowsSiloWalk(t *testing.T) {
	for logical, want := range map[string]bool{
		"Heat (1995) {tmdb-949}/Heat (1995).mkv":                 false,
		"Heat (1995) {tmdb-949}/Trailers/trailer.mkv":            true,
		"Heat (1995) {tmdb-949}/Extras/Making Of/part.mkv":       true,
		"Heat (1995) {tmdb-949}/Heat (1995)-trailer.mkv":         true,
		"Heat (1995) {tmdb-949}/Sample/heat.mkv":                 true,
		"Heat (1995) {tmdb-949}/sample.mkv":                      true,
		".recyclebin/Heat (1995)/Heat (1995).mkv":                true,
		"00s/.downloads/Heat (1995).mkv":                         true,
		"@eaDir/Heat (1995).mkv":                                 true,
		"shorts/Heat (1995)/Heat (1995).mkv":                     false,
		"Other/Heat (1995) {tmdb-949}/Heat (1995).mkv":           false,
		"3 A.M. (2001) {imdb-tt0248661} {tmdb-77084}/3 A.M..mkv": false,
	} {
		if got := StorageMovieFileSkipped(storageFileEntry(logical)); got != want {
			t.Errorf("%s: skipped=%v want %v", logical, got, want)
		}
	}
}

// Skipped directories are never listed, so their sidecars and extras cost no
// provider requests. Movie-only folder conventions do not apply to books.
func TestStorageDirectorySkipped(t *testing.T) {
	for _, tc := range []struct {
		kind, logical string
		want          bool
	}{
		{"movies", ".recyclebin", true},
		{"movies", "00s/@eaDir", true},
		{"movies", "00s/$RECYCLE.BIN", true},
		{"movies", "Heat (1995) {tmdb-949}/Trailers", true},
		{"movies", "Heat (1995) {tmdb-949}/Subs", true},
		{"movies", "Heat (1995) {tmdb-949}/Sample", true},
		{"movies", "00s", false},
		{"movies", "shorts", false},
		{"movies", "Extras", false},
		{"movies", "4K/Other", false},
		{"movies", "Heat (1995) {tmdb-949}", false},
		{"ebooks", ".recyclebin", true},
		{"ebooks", "Author/Trailers", false},
		{"ebooks", "Author/Subs", false},
	} {
		if got := StorageDirectorySkipped(tc.kind, storageDirectoryEntry(tc.logical)); got != tc.want {
			t.Errorf("%s %s: skipped=%v want %v", tc.kind, tc.logical, got, tc.want)
		}
	}
	if StorageDirectorySkipped("movies", storageFileEntry(".recyclebin/x.mkv")) {
		t.Fatal("a file entry is not a listed directory")
	}
}

// Split movies keep Silo's part metadata: a part total needs two parts of
// one item and presentation group, as variant finalization requires.
func TestStorageMoviePartTotals(t *testing.T) {
	ids := storageMovieIdentities([]*storagev1.Entry{
		storageFileEntry("Ran (1985)/Ran (1985) - cd1.mkv"),
		storageFileEntry("Ran (1985)/Ran (1985) - cd2.mkv"),
		storageFileEntry("Ronin (1998)/Ronin (1998) - cd2.mkv"),
	})
	files := make([]models.MediaFile, len(ids))
	for i, id := range ids {
		files[i].ContentID = "item-" + id.title
		applyStorageMovieVariant(&files[i], id.variant)
	}
	applyStorageMoviePartTotals(files)
	if ids[0].groupKey != ids[1].groupKey || files[0].PresentationKind != "multipart_movie" ||
		files[0].PresentationPartIndex != 1 || files[1].PresentationPartIndex != 2 ||
		files[0].PresentationPartTotal != 2 || files[1].PresentationPartTotal != 2 || files[2].PresentationPartTotal != 0 {
		t.Fatalf("parts %+v", files)
	}
}
