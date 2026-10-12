package librarykind

// SupportsNativeStorage keeps movie admission held until the host explicitly
// confirms playback readiness at startup. Mixed and series libraries remain
// outside this movie integration.
func SupportsNativeStorage(libraryType string, nativeMovieReady bool) bool {
	return SupportsNativeStorageKinds(libraryType, nativeMovieReady, false)
}

// SupportsNativeStorageKinds admits only composed native media lanes.
func SupportsNativeStorageKinds(libraryType string, movies, audiobooks bool) bool {
	return IsEbook(libraryType) || movies && IsMovie(libraryType) || audiobooks && IsAudiobook(libraryType)
}
