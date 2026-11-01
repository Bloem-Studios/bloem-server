package api

import (
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
