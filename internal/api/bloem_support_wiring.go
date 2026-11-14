package api

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/mail"
	"github.com/Silo-Server/silo-server/internal/passwordreset"
	"github.com/Silo-Server/silo-server/internal/supportaccess"
)

// supportAccessValidator adapts the break-glass record to the middleware,
// which names its "not valid" answer without importing the record.
type supportAccessValidator struct{ store *supportaccess.Store }

func (v supportAccessValidator) ValidateSupportAccess(ctx context.Context, accessID, organizationID uuid.UUID, accountID int, loginSessionID string) (int64, error) {
	revision, err := v.store.ValidateSupportAccess(ctx, accessID, organizationID, accountID, loginSessionID)
	if errors.Is(err, supportaccess.ErrNotFound) {
		return 0, apimw.ErrSupportAccessInvalid
	}
	return revision, err
}

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
	support := handlers.NewBloemOrganizationSupportHandler(deps.DB, people, resets, admin)
	// Streams read through Silo's session loader with the organization filter,
	// and stop or message through Silo's own administrator services. Typed nil
	// pointers must not become non-nil interfaces.
	var sessions handlers.SupportStreamSessions
	if admin != nil && admin.AdminPlaybackSessionsAvailable() {
		sessions = admin
	}
	support.SetStreams(sessions, deps.AdminPlaybackControl)
	support.SetSupportAccess(supportaccess.NewStore(deps.DB))
	return support
}
