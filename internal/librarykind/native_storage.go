package librarykind

// SupportsNativeStorage keeps movie admission held until the host explicitly
// confirms playback readiness at startup. Mixed and series libraries remain
// outside this movie integration.
func SupportsNativeStorage(libraryType string, nativeMovieReady bool) bool {
	return IsEbook(libraryType) || (nativeMovieReady && IsMovie(libraryType))
}
