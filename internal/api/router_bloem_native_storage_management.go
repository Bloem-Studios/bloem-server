package api

import (
	"encoding/json"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/go-chi/chi/v5"
)

// mountBloemNativeStorageManagement registers only inside the existing
// /api/bloem/v1/admin subtree AFTER AdminContextMiddleware.Require. It does not
// mount a second auth/session stack or attach production dependencies.
func mountBloemNativeStorageManagement(r chi.Router, h *handlers.BloemNativeStorageManagementHandler) {
	for _, scope := range []struct {
		prefix string
		scope  auth.AdminScope
	}{
		{"/platform/native-storage", auth.AdminScopePlatform},
		{"/organization/native-storage", auth.AdminScopeOrganization},
	} {
		handler := h.ForAdminScope(scope.scope)
		r.Route(scope.prefix, func(r chi.Router) {
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
			r.Delete("/installations/{installation_id}", handler.HandleUninstall)
			r.Get("/sources/{source_key}/bindings", handler.HandleBindings)
			r.Put("/sources/{source_key}/bindings/{library_id}", handler.HandleBind)
			r.Post("/libraries", handler.HandleCreateLibrary)
			r.Get("/libraries", handler.HandleListLibraries)
			r.Get("/libraries/creation/{creation_key}", handler.HandleGetLibraryByCreationKey)
			r.Get("/libraries/{library_id}", handler.HandleGetLibrary)
			r.Post("/libraries/{library_id}/initialize", handler.HandleInitializeLibrary)
			r.Post("/libraries/{library_id}/scan", handler.HandleScanLibrary)
		})
	}
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

// nativeStorageOnboardingDependencies is startup-only, before handlers capture
// Dependencies. It reuses the host registry, folder store and running queue;
// constructing these services is not a readiness witness.
func nativeStorageOnboardingDependencies(deps Dependencies) Dependencies {
	if deps.NativeStorageManagement == nil {
		if deps.DB == nil || deps.NativeStorage == nil || deps.NativeStorage.Registry == nil || deps.FolderRepo == nil {
			return deps
		}
		sources := nativestorage.NewSourceManagement(deps.DB, deps.NativeStorage.Registry)
		libraries := nativestorage.NewLibraryManagement(deps.DB, deps.FolderRepo,
			sections.NewRepository(deps.DB), resourcetenancy.NewStore(deps.DB), deps.LibraryScanQueue)
		h := handlers.NewBloemNativeStorageManagementHandler(sources, libraries)
		h.Registry = deps.NativeStorage.Registry
		deps.NativeStorageManagement = h
	} else {
		// Services remain caller-owned; each router gets its own handler/witness.
		h := *deps.NativeStorageManagement
		deps.NativeStorageManagement = &h
	}
	// Scope-local handler copies are mounted before final v2 composition. They
	// must share this fresh holder, which wrapV2 seals before routing is exposed.
	holder := &nativeStorageCapabilities{}
	deps.NativeStorageManagement.Capabilities = holder
	holder.deps = deps
	return deps
}

// attachNativeStorageOnboardingReader attaches the existing reader instance.
// A missing reader or different implementation never allocates a replacement.
func attachNativeStorageOnboardingReader(deps Dependencies, files *handlers.NativeEbookFileService) bool {
	if deps.NativeStorageManagement == nil || deps.NativeStorageManagement.Sources == nil || files == nil {
		return false
	}
	coordinator, ok := files.Native.(*nativestorage.Coordinator)
	if !ok || coordinator == nil {
		return false
	}
	deps.NativeStorageManagement.Sources.SetCoordinator(coordinator)
	return true
}
