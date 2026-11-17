package plugins

import "net/http"

// BloemContentDenials is Bloem's refusal state on ContentAccess, embedded
// there by one line so its fields are promoted (access.PlaybackDenied). Bloem's
// plugin boundary (internal/api/bloem_plugin_content_access.go) sets them.
type BloemContentDenials struct {
	// PlaybackDenied prevents an authenticated browse-only viewer from spending
	// their identity on arbitrary plugin byte access, including public descriptors.
	PlaybackDenied bool
}

// refuseBloemContent answers 403 and reports true when Bloem's boundary denied
// the request. Denial stays distinct from anonymous access: a refused viewer
// does not fall back to a public descriptor.
func refuseBloemContent(w http.ResponseWriter, access ContentAccess) bool {
	if !access.PlaybackDenied {
		return false
	}
	http.Error(w, "Plugin content access has not been granted", http.StatusForbidden)
	return true
}
