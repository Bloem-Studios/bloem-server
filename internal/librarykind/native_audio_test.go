package librarykind

import "testing"

func TestNativeStorageAudioReadiness(t *testing.T) {
	for _, tc := range []struct {
		kind                string
		movies, audio, want bool
	}{
		{"audiobooks", false, false, false}, {"audiobooks", true, false, false}, {"audiobooks", false, true, true}, {"movies", true, false, true}, {"ebooks", false, false, true}, {"series", true, true, false},
	} {
		if got := SupportsNativeStorageKinds(tc.kind, tc.movies, tc.audio); got != tc.want {
			t.Fatalf("%+v got=%v", tc, got)
		}
	}
	if SupportsNativeStorage("audiobooks", true) {
		t.Fatal("legacy wrapper admitted audio")
	}
}
