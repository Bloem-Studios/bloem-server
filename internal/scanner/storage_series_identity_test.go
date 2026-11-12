package scanner

import (
	"testing"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// The verified ClearStream layout is
// television/<group>/<Show (Year) {tvdb-ID}>/Season NN/<Show (Year)> - SxxEyy [quality]-Group.mkv.
const (
	showA = "television/00s/Show A (2005) {tvdb-111}/"
	showB = "television/10s/Show B (2012) {tvdb-222}/"
)

// Show, season and episode come from Silo's own naming rules applied to the
// logical path: a decade group folder must not become the show, the folder's
// {tvdb-} tag must reach matching, and specials keep season 0.
func TestStorageSeriesIdentityReadsTheVerifiedLayout(t *testing.T) {
	for _, tc := range []struct {
		logical             string
		roots               []string
		title               string
		year                int
		tvdb                string
		season, episode     int
		seasonKnown         bool
		multiStart, multiTo int
	}{
		{showA + "Season 01/Show A (2005) - S01E01 [WEBDL-1080p]-Grp.mkv", nil, "Show A", 2005, "111", 1, 1, true, 0, 0},
		{showA + "Season 01/Show A (2005) - S01E02 [WEBDL-1080p]-Grp.mkv", []string{"television"}, "Show A", 2005, "111", 1, 2, true, 0, 0},
		{showA + "Season 01/Show A (2005) - S01E02 [WEBDL-1080p]-Grp.mkv", []string{"television/00s"}, "Show A", 2005, "111", 1, 2, true, 0, 0},
		// A library rooted at the show folder itself keeps the folder's tag.
		{showA + "Season 02/Show A (2005) - S02E03 [HDTV-720p]-Grp.mkv", []string{"television/00s/Show A (2005) {tvdb-111}"}, "Show A", 2005, "111", 2, 3, true, 0, 0},
		{showA + "Season 00/Show A (2005) - S00E01 [WEBDL-1080p]-Grp.mkv", nil, "Show A", 2005, "111", 0, 1, true, 0, 0},
		{showB + "Season 02/Show B (2012) - S02E01-E02 [HDTV-720p]-Grp.mkv", nil, "Show B", 2012, "222", 2, 1, true, 1, 2},
		{"television/4k/Show C (2001)/Show C (2001) - S01E03 [Bluray-2160p]-Grp.mkv", nil, "Show C", 2001, "", 1, 3, true, 0, 0},
	} {
		got := storageSeriesIdentities([]*storagev1.Entry{storageFileEntry(tc.logical)}, StorageSeriesParseRoots(selectedRoots(tc.roots...)))[0]
		if !got.episodic || got.title != tc.title || got.year != tc.year || got.tvdbID != tc.tvdb || got.season != tc.season ||
			got.episode != tc.episode || got.seasonKnown != tc.seasonKnown || got.multiStart != tc.multiStart || got.multiEnd != tc.multiTo || got.groupKey == "" {
			t.Errorf("%s %v: got %+v", tc.logical, tc.roots, got)
		}
	}
}

// Seasons of one show arrive on different listing pages. They must share
// Silo's content group so every page joins the same series item, while two
// shows never share one.
func TestStorageSeriesIdentityGroupsAcrossPages(t *testing.T) {
	first := storageSeriesIdentities([]*storagev1.Entry{storageFileEntry(showA + "Season 01/Show A (2005) - S01E01 [WEBDL-1080p]-Grp.mkv")}, nil)[0]
	second := storageSeriesIdentities([]*storagev1.Entry{storageFileEntry(showA + "Season 00/Show A (2005) - S00E01 [WEBDL-1080p]-Grp.mkv")}, nil)[0]
	other := storageSeriesIdentities([]*storagev1.Entry{storageFileEntry(showB + "Season 01/Show B (2012) - S01E01 [HDTV-720p]-Grp.mkv")}, nil)[0]
	if first.groupKey != second.groupKey || first.groupKeyVersion != second.groupKeyVersion || first.groupKey == other.groupKey {
		t.Fatalf("group keys %q %q %q", first.groupKey, second.groupKey, other.groupKey)
	}
}

// Files Silo cannot link to an episode by number stay journal entries: an
// air-date name needs the path-parsing fallback, which an opaque catalog key
// cannot feed.
func TestStorageSeriesIdentityLeavesUnnumberedFilesUnpublished(t *testing.T) {
	for _, logical := range []string{
		"television/00s/Daily (2010)/Season 2020/Daily (2010) - 2020-01-02.mkv",
		showA + "Show A (2005) - Behind the Scenes.mkv",
	} {
		if got := storageSeriesIdentities([]*storagev1.Entry{storageFileEntry(logical)}, nil)[0]; got.episodic {
			t.Errorf("%s published as episode %+v", logical, got)
		}
	}
}

// Extras, samples, hidden folders (the live .downloads/) and zero-byte marker
// objects are never probed or published as episodes.
func TestStorageSeriesFileSkipped(t *testing.T) {
	for logical, want := range map[string]bool{
		showA + "Season 01/Show A (2005) - S01E01 [WEBDL-1080p]-Grp.mkv":          false,
		showA + "Featurettes/Making Of.mkv":                                       true,
		showA + "Season 01/sample.mkv":                                            true,
		showA + "Season 01/Show A (2005) - S01E01 [WEBDL-1080p]-Grp-sample.mkv":   true,
		"television/00s/.downloads/Show A (2005) - S01E04 [WEBDL-1080p]-Grp.mkv":  true,
		showA + "Season 01/.partial/Show A (2005) - S01E05 [WEBDL-1080p]-Grp.mkv": true,
	} {
		entry := storageFileEntry(logical)
		entry.Size = 1
		if got := StorageSeriesFileSkipped(entry); got != want {
			t.Errorf("%s skipped=%v want %v", logical, got, want)
		}
	}
	marker := storageFileEntry(showA + "Season 01/Show A (2005) - S01E06 [WEBDL-1080p]-Grp.mkv")
	if !StorageSeriesFileSkipped(marker) {
		t.Error("zero-byte object published as an episode")
	}
	for logical, want := range map[string]bool{
		"television/00s/.downloads":   true,
		showA + "Featurettes":         true,
		showA + "Season 01":           false,
		"television/00s":              false,
		showA + "Season 01/Subs/.git": true,
	} {
		if got := StorageDirectorySkipped("series", storageDirectoryEntry(logical)); got != want {
			t.Errorf("directory %s skipped=%v want %v", logical, got, want)
		}
	}
}

// Sidecars attach to the episode whose base name they extend, with Silo's
// language and SDH tags; images and nfo files are never subtitles.
func TestStorageSeriesSidecarsPairWithTheirEpisode(t *testing.T) {
	base := showA + "Season 01/Show A (2005) - S01E01 [WEBDL-1080p]-Grp"
	video := storageFileEntry(base + ".mkv")
	other := storageFileEntry(showA + "Season 01/Show A (2005) - S01E01 [WEBDL-1080p]-Grp2.mkv")
	var subs []StorageSeriesSidecar
	for _, logical := range []string{base + ".en.srt", base + ".en.sdh.srt", base + ".nl.forced.srt", showA + "poster.jpg", showA + "tvshow.nfo", base + ".nfo"} {
		if sub, ok := StorageSeriesSidecarFromEntry(storageFileEntry(logical)); ok {
			subs = append(subs, sub)
		}
	}
	if len(subs) != 3 {
		t.Fatalf("sidecars %+v", subs)
	}
	paired := PairStorageSeriesSidecars([]*storagev1.Entry{video, other}, subs)
	got := paired[video.GetId()]
	if len(got) != 3 || len(paired[other.GetId()]) != 0 {
		t.Fatalf("paired %+v", paired)
	}
	if got[0].Language != "en" || got[0].HearingImpaired || got[1].Language != "en" || !got[1].HearingImpaired || got[2].Language != "nl" || !got[2].Forced || got[0].Format != "srt" {
		t.Fatalf("tags %+v", got)
	}
}

func selectedRoots(paths ...string) []storagesource.SelectedRoot {
	roots := make([]storagesource.SelectedRoot, len(paths))
	for i, p := range paths {
		roots[i] = storagesource.SelectedRoot{LogicalPath: p, Enabled: true}
	}
	return roots
}
