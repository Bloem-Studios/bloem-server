package api

import (
	"encoding/json"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/go-chi/chi/v5"
)

// mountBloemNativeStorageManagement registers only inside the existing
// /api/bloem/v1/admin subtree AFTER AdminContextMiddleware.Require. It does not
// mount a second auth/session stack or attach production dependencies.
func mountBloemNativeStorageManagement(r chi.Router, h *handlers.BloemNativeStorageManagementHandler) {
	// Each scope is mounted with a literal prefix so the route inventory
	// generator can follow it.
	r.Route("/platform/native-storage", func(r chi.Router) {
		mountNativeStorageScopeRoutes(r, h.ForAdminScope(auth.AdminScopePlatform))
	})
	r.Route("/organization/native-storage", func(r chi.Router) {
		mountNativeStorageScopeRoutes(r, h.ForAdminScope(auth.AdminScopeOrganization))
	})
}

func mountNativeStorageScopeRoutes(r chi.Router, handler *handlers.BloemNativeStorageManagementHandler) {
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		nativeStorageRouteError(w, &handlers.APIError{Status: 404, Code: "not_found", Message: "Resource not found"})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		nativeStorageRouteError(w, &handlers.APIError{Status: 405, Code: "method_not_allowed", Message: "Method not allowed"})
	})
	r.Get("/capabilities", handler.HandleCapabilities)
	r.Get("/artifacts", handler.HandleArtifacts)
	r.Get("/sources", handler.HandleListSources)
	r.Get("/sources/{source_key}", handler.HandleGetSource)
	r.Post("/installations", handler.HandleInstall)
	r.Put("/sources/{source_key}/configuration", handler.HandleReplaceConfiguration)
	r.Post("/installations/{installation_id}/disable", handler.HandleDisable)
	r.Post("/installations/{installation_id}/upgrade", handler.HandleUpgrade)
	r.Delete("/installations/{installation_id}", handler.HandleUninstall)
}

// Native v2 adapters use this direct Problem; the generic status translator
// would discard the native code. No mutation decorators are installed in C1.
func nativeStorageProblem(err error) *apiv2.Problem {
	decision := handlers.NativeStorageAPIError(err)
	if decision == nil {
		return nil
	}
	kind := apiv2.ProblemType{ID: decision.Code, Status: decision.Status, Title: decision.Message}
	return apiv2.NewProblem(kind, decision.Message).WithHeader("Cache-Control", "no-store")
}

// Router fallbacks do not reach a handler, so they need the same bounded
// Bloem envelope and privacy headers as management handler responses.
func nativeStorageRouteError(w http.ResponseWriter, decision *handlers.APIError) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(decision.Status)
	_ = json.NewEncoder(w).Encode(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}{decision.Code, decision.Message})
}

// nativeStorageDependencies is startup-only, before handlers capture
// Dependencies. It builds the storage source management handler on the host
// registry; each router gets its own handler and capability witness.
func nativeStorageDependencies(deps Dependencies) Dependencies {
	if deps.NativeStorageManagement == nil {
		if deps.DB == nil || deps.NativeStorage == nil || deps.NativeStorage.Registry == nil {
			return deps
		}
		h := handlers.NewBloemNativeStorageManagementHandler(nativestorage.NewSourceManagement(deps.DB, deps.NativeStorage.Registry))
		h.Registry = deps.NativeStorage.Registry
		deps.NativeStorageManagement = h
	} else {
		h := *deps.NativeStorageManagement
		deps.NativeStorageManagement = &h
	}
	deps.NativeStorageManagement.Capabilities = &nativeStorageCapabilities{deps: deps}
	return deps
}

// attachNativeStorageReader shares the reader's coordinator with the artwork
// route, which opens storage covers through it, and with source management, so
// disabling a source fences its open files; it also marks the router composed.
func attachNativeStorageReader(deps Dependencies, files *handlers.NativeEbookFileService) {
	if files == nil {
		return
	}
	coordinator, ok := files.Native.(*nativestorage.Coordinator)
	if !ok || coordinator == nil {
		return
	}
	deps.NativeStorage.SetCoverReader(coordinator)
	if deps.NativeStorageManagement == nil || deps.NativeStorageManagement.Sources == nil {
		return
	}
	deps.NativeStorageManagement.Sources.SetCoordinator(coordinator)
	if capabilities, ok := deps.NativeStorageManagement.Capabilities.(*nativeStorageCapabilities); ok && capabilities != nil {
		capabilities.reader = true
	}
}
