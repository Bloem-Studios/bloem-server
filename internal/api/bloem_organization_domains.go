package api

import (
	"context"
	"strings"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/mail"
	"github.com/Silo-Server/silo-server/internal/orgdomains"
)

// bloemAccountMailSettings is the settings reader account mail (invitations
// and password reset) builds its links and server name from. With the domain
// directory wired, an organization's emails link to its primary domain and
// carry its name; without it, every email uses the server's.
func bloemAccountMailSettings(deps Dependencies, settings catalog.SettingsStore) mail.SettingReader {
	if deps.OrganizationDomains == nil {
		return settings
	}
	return orgdomains.NewSettingsOverlay(settings, deps.OrganizationDomains, deps.OrganizationDomainStore, orgdomains.OverlayOptions{PublicURL: true})
}

// bloemInstallSignInDomainCheck refuses a sign-in on another organization's
// domain, when the domain directory is wired.
func bloemInstallSignInDomainCheck(deps Dependencies, authService *auth.Service, settings catalog.SettingsStore) {
	if authService == nil || deps.OrganizationDomains == nil || deps.OrganizationDomainStore == nil {
		return
	}
	authService.SetSignInDomainCheck(deps.OrganizationDomains.SignInCheck(deps.OrganizationDomainStore, bloemServerPublicURL(settings)))
}

// bloemInstallOAuthDomains keeps a web sign-in through an OAuth provider on the
// organization domain it started on, when the domain directory is wired.
func bloemInstallOAuthDomains(deps Dependencies, oauthHandler *auth.OAuthHandler, settings catalog.SettingsStore) {
	if oauthHandler == nil || deps.OrganizationDomains == nil {
		return
	}
	oauthHandler.SetRequestBaseURL(deps.OrganizationDomains.OAuthBaseURL(bloemServerPublicURL(settings)))
}

// bloemServerPublicURL reads server.public_url at the time it is asked.
func bloemServerPublicURL(settings catalog.SettingsStore) func(context.Context) string {
	return func(ctx context.Context) string {
		if settings == nil {
			return ""
		}
		value, err := settings.Get(ctx, "server.public_url")
		if err != nil {
			return ""
		}
		return strings.TrimRight(strings.TrimSpace(value), "/")
	}
}
