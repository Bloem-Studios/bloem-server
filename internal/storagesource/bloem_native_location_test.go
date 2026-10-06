package storagesource

import "testing"

func TestNativeContainmentNamespace(t *testing.T) {
	for _, tc := range []struct {
		path   string
		native bool
	}{
		{"bloem-storage:", true}, {"bloem-storage:malformed", true}, {"bloem-storage:../physical", true},
		{"bloem-storage:" + string(make([]byte, 64)), true}, {"", false}, {"/books/bloem-storage:key", false}, {"./bloem-storage:key", false}, {"books/bloem-storage:key", false}, {"BLOEM-STORAGE:key", false},
	} {
		if got := IsNativeLocation(tc.path); got != tc.native {
			t.Errorf("path=%q native=%v want=%v", tc.path, got, tc.native)
		}
	}
}
