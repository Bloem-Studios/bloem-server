package plugins

import "net/http"

// WrapAccess applies wrap to both of the handler's access resolvers (plugin
// pages and plugin assets), so the router can keep Silo's construction and add
// Bloem's content boundary on its own line.
func (h *ContentHandler) WrapAccess(wrap func(func(*http.Request) ContentAccess) func(*http.Request) ContentAccess) {
	if h == nil || wrap == nil {
		return
	}
	if h.routeAccess != nil {
		h.routeAccess = wrap(h.routeAccess)
	}
	if h.assetAccess != nil {
		h.assetAccess = wrap(h.assetAccess)
	}
}
