package librarykind

import "testing"

// Bloem's music library kind, kept out of Silo's librarykind_test.go.
func TestBloemMusicKind(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"music", true},
		{" Songs ", true},
		{"audio", false},
		{"audiobooks", false},
	} {
		if got := IsMusic(tc.in); got != tc.want {
			t.Errorf("IsMusic(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if got := Of(" music "); got != (Kinds{Music: true}) {
		t.Errorf("Of(music) = %+v", got)
	}
}
