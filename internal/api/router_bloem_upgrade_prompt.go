package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

// bloemUpgradePromptHandler serves the upgrade prompt settings, and installs
// the notifier that turns a stream refused at the plan's limit into an inbox
// alert. Without a database neither exists.
func bloemUpgradePromptHandler(deps Dependencies) *handlers.BloemUpgradePromptHandler {
	if deps.DB == nil {
		return nil
	}
	if deps.SessionMgr != nil {
		if notifier := notifications.NewUpgradePromptNotifier(deps.Notifications); notifier != nil {
			deps.SessionMgr.SetStreamLimitObserver(notifier)
		}
	}
	return handlers.NewBloemUpgradePromptHandler(deps.DB)
}

// mountBloemUpgradePrompt lets a Platform context or an administrator's
// unscoped API key read and replace the upgrade prompt, which is how a
// storefront points it at its plan page.
func mountBloemUpgradePrompt(r chi.Router, h *handlers.BloemUpgradePromptHandler, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware) {
	if h == nil || authMW == nil || adminMW == nil {
		return
	}
	r.Group(func(r chi.Router) {
		eitherPlatformCredential := bloemEitherPlatformCredential(authMW, adminMW)
		r.Use(eitherPlatformCredential)
		r.Get("/admin/platform/upgrade-prompt", h.HandleGet)
		r.Put("/admin/platform/upgrade-prompt", h.HandlePut)
	})
}
