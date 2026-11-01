package orgdomains

import (
	"context"
	"net/url"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// SignInCheck refuses a sign-in that arrived on a domain registered to an
// organization other than the account's. A request on an unregistered host
// (an IP address, a LAN name) is never refused, so an administrator can
// always reach the server. The refusal names the account's primary domain,
// or serverPublicURL when its organization has none; such an account is let in
// on the host of serverPublicURL.
func (d *Directory) SignInCheck(store *Store, serverPublicURL func(context.Context) string) auth.SignInDomainCheck {
	return func(ctx context.Context, accountID int) error {
		hostOrg, ok := RequestOrganization(ctx)
		if !ok {
			return nil
		}
		accountOrg, found, err := store.AccountOrganization(ctx, accountID)
		if err != nil {
			return err
		}
		if !found || accountOrg == hostOrg {
			return nil
		}
		server := ""
		if serverPublicURL != nil {
			server = serverPublicURL(ctx)
		}
		serverURL, parseErr := url.Parse(server)
		if host, ok := d.PrimaryHost(ctx, accountOrg); ok {
			scheme := "https"
			if parseErr == nil && serverURL.Scheme == "http" {
				scheme = "http"
			}
			return &auth.OrganizationDomainError{SignInURL: scheme + "://" + host}
		}
		// Without a domain of its own, the organization signs in at the
		// server's address, even when that host is registered to another
		// organization. Refusing it there would name the address that refused.
		if requestHost, ok := RequestDomain(ctx); ok && parseErr == nil && requestHost == requestHostKey(serverURL.Host) {
			return nil
		}
		return &auth.OrganizationDomainError{SignInURL: server}
	}
}
