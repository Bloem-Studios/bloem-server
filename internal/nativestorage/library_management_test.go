package nativestorage

import "testing"

func TestNativeOnboardingLibraryRepeatRevisions(t *testing.T) {
	for _, tc := range []struct {
		name              string
		expected, current int64
		bound, want       bool
	}{
		{"L1", 1, 1, false, true}, {"L2-repeat", 2, 2, false, true}, {"L2-lost-response", 1, 2, false, true},
		{"L3-current", 3, 3, true, true}, {"bound-old", 2, 3, true, false}, {"unbound-other-old", 1, 3, false, false},
		{"zero", 0, 1, false, false}, {"future", 4, 3, true, false},
	} {
		t.Run("init-"+tc.name, func(t *testing.T) {
			if got := nativeInitializeRevisionAllowed(tc.expected, tc.current, tc.bound); got != tc.want {
				t.Fatalf("revision admitted=%v want=%v", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name              string
		expected, current int64
		exact, want       bool
	}{
		{"first", 2, 2, false, true}, {"repeat", 3, 3, true, true}, {"lost-response", 2, 3, true, true},
		{"foreign-old", 2, 3, false, false}, {"foreign-current", 3, 3, false, true}, {"older", 1, 3, true, false},
		{"zero", 0, 2, false, false}, {"future", 4, 3, true, false},
	} {
		t.Run("bind-"+tc.name, func(t *testing.T) {
			if got := nativeBindRevisionAllowed(tc.expected, tc.current, tc.exact); got != tc.want {
				t.Fatalf("revision admitted=%v want=%v", got, tc.want)
			}
		})
	}
}
