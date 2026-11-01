package orgdomains

import (
	"context"
	"net/url"
)

// OAuthBaseURL names, for auth.OAuthHandler.SetRequestBaseURL, the base URL
// of the registered domain a request arrived on, in the scheme of
// serverPublicURL (https when it has none). It is empty off a registered
// domain, where a sign-in keeps the public URL. Each domain's callback must
// be registered with the provider.
func (d *Directory) OAuthBaseURL(serverPublicURL func(context.Context) string) func(context.Context) string {
	return func(ctx context.Context) string {
		if d == nil {
			return ""
		}
		host, ok := RequestDomain(ctx)
		if !ok {
			return ""
		}
		scheme := "https"
		if serverPublicURL != nil {
			if u, err := url.Parse(serverPublicURL(ctx)); err == nil && u.Scheme == "http" {
				scheme = "http"
			}
		}
		return scheme + "://" + host
	}
}
