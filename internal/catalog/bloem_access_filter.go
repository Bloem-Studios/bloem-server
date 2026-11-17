package catalog

import (
	"github.com/Silo-Server/silo-server/internal/models"
)

// BloemTransportDenials is Bloem's transport metadata on AccessFilter, embedded
// there by one line so its fields are promoted (filter.PlaybackDenied).
type BloemTransportDenials struct {
	// PlaybackDenied is transport metadata for compatibility surfaces. It is
	// deliberately not a catalog predicate: browse-only members can still see
	// the catalog, but cannot advertise or enter a playback transport.
	PlaybackDenied bool
	// DownloadDenied is separate from playback: Jelly's Download transport is
	// also used by some direct-play clients, but may not bypass offline-download
	// policy when a custom template permits playback and forbids downloads.
	DownloadDenied bool
}

// FileAllowedByLibraryScope reports whether a file's library membership fits
// the allowed/disabled library sets alone, ignoring quality ceiling and every
// other facet of the viewer's access policy.
//
// A delivery guard that rechecks entitlement right before serving bytes (a
// session or signed restart recipe names a source; it does not preserve a
// library grant) must call this, never FileAllowedByAccess, for exactly that
// reason: a transcode may legitimately serve a lower resolution than its
// source, and admission — not this recheck — owns that decision. Passing the
// full AccessFilter (with its MaxPlaybackQuality) into a delivery guard would
// wrongly 404 a viewer capped below the source's resolution even though their
// library membership is fine.
func FileAllowedByLibraryScope(file *models.MediaFile, allowedLibraryIDs []int, disabledLibraryIDs []int) bool {
	if file == nil {
		return false
	}
	if allowedLibraryIDs != nil && !intInSlice(file.MediaFolderID, allowedLibraryIDs) {
		return false
	}
	if len(disabledLibraryIDs) > 0 && intInSlice(file.MediaFolderID, disabledLibraryIDs) {
		return false
	}
	return true
}
