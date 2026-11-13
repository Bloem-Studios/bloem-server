package api

import (
	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/mail"
	"github.com/Silo-Server/silo-server/internal/passwordreset"
)

// newBloemSupportHandler builds the organization support routes' handler. It
// has its own reset-link service over the same tables as Silo's: the routes
// never complete a link (that is Silo's public route) so it needs no sign-in
// service, only the settings overlay that makes links and emails follow the
// target organization's domain and name.
func newBloemSupportHandler(deps Dependencies, people *adminpeople.Service, admin *handlers.AdminHandler) *handlers.BloemOrganizationSupportHandler {
	if deps.DB == nil || people == nil {
		return nil
	}
	settings := catalog.NewEncryptedSettingsRepo(catalog.NewServerSettingsRepo(deps.DB), deps.SecretCipher)
	sender := deps.SupportMail
	if sender == nil {
		sender = mail.NewSMTPSender(settings)
	}
	resets := passwordreset.NewService(
		passwordreset.NewRepository(deps.DB),
		auth.NewUserRepository(deps.DB),
		nil,
		sender,
		bloemAccountMailSettings(deps, settings),
		deps.EmailBrand,
		"",
	)
	return handlers.NewBloemOrganizationSupportHandler(deps.DB, people, resets, admin)
}
