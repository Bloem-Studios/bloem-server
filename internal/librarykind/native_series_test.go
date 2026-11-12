package librarykind

import "testing"

// Series libraries are admitted only while the series lane is composed, and
// that lane never admits movies, audiobooks or mixed libraries on its own.
func TestNativeStorageSeriesReadiness(t *testing.T) {
	for _, tc := range []struct {
		kind                        string
		movies, audio, series, want bool
	}{
		{"series", false, false, false, false},
		{"series", true, true, false, false},
		{"series", false, false, true, true},
		{"tv", false, false, true, true},
		{"tvshows", false, false, true, true},
		{"mixed", true, true, true, false},
		{"movies", false, false, true, false},
		{"audiobooks", false, false, true, false},
		{"ebooks", false, false, false, true},
		{"movies", true, false, false, true},
	} {
		if got := SupportsNativeStorageLanes(tc.kind, tc.movies, tc.audio, tc.series); got != tc.want {
			t.Fatalf("%+v got=%v", tc, got)
		}
	}
	if SupportsNativeStorageKinds("series", true, true) {
		t.Fatal("the movie and audio lanes admitted a series library")
	}
}
