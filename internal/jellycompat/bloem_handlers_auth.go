package jellycompat

// bloemAuthLiveTV is AuthHandler's Live TV advertisement state, embedded so
// upstream's field list stays untouched.
type bloemAuthLiveTV struct {
	liveTVEnabled bool
	liveTVAccess  LiveTVAccessResolver
}

// SetLiveTVEnabled advertises Live TV access when the shared service is wired.
func (h *AuthHandler) SetLiveTVEnabled(enabled bool) { h.liveTVEnabled = enabled }
