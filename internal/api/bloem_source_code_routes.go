package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// mountBloemSourceCode is called inside /api/bloem/v2. Any signed-in account
// may read the running version's source code, household logins included; it
// needs no profile, organization or admin role.
func mountBloemSourceCode(r chi.Router, handler *handlers.BloemSourceCodeHandler, system *handlers.BloemSystemHandler, authMW *apimw.AuthMiddleware) {
	available := handler.Available() && authMW != nil
	if system != nil {
		system.SetSourceCodeAvailable(available)
	}
	if !available {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(authMW.RequireAuth)
		r.Get("/source-code", handler.HandleGet)
		r.Get("/source-code/archive", handler.HandleArchive)
	})
}
