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
// or serverPublicURL when its organization has none.
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
		if host, ok := d.PrimaryHost(ctx, accountOrg); ok {
			scheme := "https"
			if u, err := url.Parse(server); err == nil && u.Scheme == "http" {
				scheme = "http"
			}
			return &auth.OrganizationDomainError{SignInURL: scheme + "://" + host}
		}
		return &auth.OrganizationDomainError{SignInURL: server}
	}
}
