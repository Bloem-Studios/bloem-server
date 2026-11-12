package librarykind

// SupportsNativeStorage keeps movie admission held until the host explicitly
// confirms playback readiness at startup. Mixed and series libraries remain
// outside this movie integration.
func SupportsNativeStorage(libraryType string, nativeMovieReady bool) bool {
	return SupportsNativeStorageKinds(libraryType, nativeMovieReady, false)
}

// SupportsNativeStorageKinds admits only composed native media lanes.
func SupportsNativeStorageKinds(libraryType string, movies, audiobooks bool) bool {
	return SupportsNativeStorageLanes(libraryType, movies, audiobooks, false)
}

// SupportsNativeStorageLanes is SupportsNativeStorageKinds with the series
// lane. A dedicated TV library is admitted only while series scanning and
// native video playback are both composed; mixed libraries never are.
func SupportsNativeStorageLanes(libraryType string, movies, audiobooks, series bool) bool {
	return IsEbook(libraryType) || movies && IsMovie(libraryType) || audiobooks && IsAudiobook(libraryType) || series && IsTV(libraryType)
}
