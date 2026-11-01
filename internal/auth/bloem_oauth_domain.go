package auth

// Bloem-owned. A web sign-in through an OAuth provider on an organization's
// own domain: the flow's callback and completion page follow the domain the
// request arrived on, so the browser keeps its cookies and finishes there. A
// provider sign-out started there returns to that domain's login page.
// See docs/architecture/bloem-organization-domains.md.

import (
	"context"
	"strings"
)

// SetRequestBaseURL installs baseFor, which names the base URL
// (scheme://host) of the organization domain ctx's request arrived on, or ""
// off a registered domain. Without it every flow uses the public URL.
func (h *OAuthHandler) SetRequestBaseURL(baseFor func(context.Context) string) {
	if baseFor == nil {
		h.requestBaseURL.Store(nil)
		return
	}
	h.requestBaseURL.Store(&baseFor)
}

// baseURLFor is the base a flow on ctx's request uses: the organization
// domain's, else the public URL.
func (h *OAuthHandler) baseURLFor(ctx context.Context) string {
	if baseFor := h.requestBaseURL.Load(); baseFor != nil {
		if base := strings.TrimRight(strings.TrimSpace((*baseFor)(ctx)), "/"); base != "" {
			return base
		}
	}
	return h.currentHostBaseURL()
}

// callbackURLFor is CallbackURL on the base of ctx's request.
func (h *OAuthHandler) callbackURLFor(ctx context.Context, prefix string, installID int) string {
	return strings.Replace(h.CallbackURL(prefix, installID), h.currentHostBaseURL(), h.baseURLFor(ctx), 1)
}

// postLogoutRedirectURLFor is PostLogoutRedirectURL on the base of ctx's
// request, so a provider sign-out returns to the domain it started on. Empty,
// like PostLogoutRedirectURL, while no public URL is configured.
func (h *OAuthHandler) postLogoutRedirectURLFor(ctx context.Context) string {
	redirect := h.PostLogoutRedirectURL()
	if redirect == "" {
		return ""
	}
	return strings.Replace(redirect, h.currentHostBaseURL(), h.baseURLFor(ctx), 1)
}

// webCompletionURLFor is webCompletionURL on the base of ctx's request.
func (h *OAuthHandler) webCompletionURLFor(ctx context.Context) string {
	return h.baseURLFor(ctx) + h.deps.FrontendCompletePath
}
