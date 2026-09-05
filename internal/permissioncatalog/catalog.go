// Package permissioncatalog owns the dependency-neutral registry of
// permissions that may be assigned to users or exposed by policy masks.
package permissioncatalog

import "slices"

const (
	WatchLiveTV      = "watch_live_tv"
	MarkerEdit       = "marker_edit"
	MetadataCuration = "metadata_curation"
)

var assignable = []string{MarkerEdit, MetadataCuration, WatchLiveTV}

// Assignable returns the complete canonical permission set in stable order.
func Assignable() []string {
	result := slices.Clone(assignable)
	slices.Sort(result)
	return result
}

// IsAssignable reports whether a permission belongs to the canonical set.
func IsAssignable(permission string) bool {
	return slices.Contains(assignable, permission)
}
