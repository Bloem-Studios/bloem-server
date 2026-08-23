package music

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

func TestLibraryAllowedFailsClosedForScopedAndDisabledLibraries(t *testing.T) {
	cases := []struct {
		name   string
		id     int
		filter catalog.AccessFilter
		want   bool
	}{
		{name: "unrestricted", id: 7, want: true},
		{name: "invalid", id: 0, want: false},
		{name: "allowed unsorted", id: 7, filter: catalog.AccessFilter{AllowedLibraryIDs: []int{9, 7, 3}}, want: true},
		{name: "not allowed", id: 7, filter: catalog.AccessFilter{AllowedLibraryIDs: []int{9, 3}}, want: false},
		{name: "empty allow list", id: 7, filter: catalog.AccessFilter{AllowedLibraryIDs: []int{}}, want: false},
		{name: "disabled overrides allowed", id: 7, filter: catalog.AccessFilter{AllowedLibraryIDs: []int{7}, DisabledLibraryIDs: []int{7}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := libraryAllowed(tc.id, tc.filter); got != tc.want {
				t.Fatalf("libraryAllowed(%d, %+v) = %v, want %v", tc.id, tc.filter, got, tc.want)
			}
		})
	}
}
