package librarykind

// IsMusic reports whether the library is a dedicated music library. Generic
// "audio" remains unknown because it cannot truthfully distinguish music from
// spoken-word libraries.
func IsMusic(libraryType string) bool {
	switch normalize(libraryType) {
	case "music", "songs":
		return true
	default:
		return false
	}
}
