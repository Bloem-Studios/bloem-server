package api

import (
	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// configureBloemSectionExtensions retains host authority and promotion wire projection.
func configureBloemSectionExtensions(handler *handlers.SectionHandler, deps Dependencies) {
	if deps.Promotions != nil {
		handler.ConfigurePromotions(deps.Promotions)
	} else {
		handler.ConfigurePromotions(nil)
	}
}
