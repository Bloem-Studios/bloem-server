package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

// mountBloemNativeStorageManagement registers only inside the existing
// /api/bloem/v1/admin subtree AFTER AdminContextMiddleware.Require. It does not
// mount a second auth/session stack or attach production dependencies.
func mountBloemNativeStorageManagement(r chi.Router, h *handlers.BloemNativeStorageManagementHandler) {
	var locations *handlers.BloemLibraryLocationsHandler
	if h != nil {
		locations = h.Locations
	}
	mountBloemLibraryLocations(r, locations)
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
	r.Get("/sources/{source_key}/browse", handler.HandleBrowseSource)
	r.Post("/installations", handler.HandleInstall)
	r.Post("/installations/discover", handler.HandleDiscoverInstall)
	r.Post("/catalog/installations", handler.HandleCatalogInstall)
	r.Post("/catalog/installations/discover", handler.HandleCatalogDiscoverInstall)
	r.Post("/catalog/installations/{installation_id}/upgrade", handler.HandleCatalogUpgrade)
	r.Put("/sources/{source_key}/configuration", handler.HandleReplaceConfiguration)
	r.Post("/sources/{source_key}/credentials", handler.HandleRotateCredentials)
	r.Post("/s3/buckets", handler.HandleS3Buckets)
	r.Post("/s3/folders", handler.HandleS3Folders)
	r.Post("/installations/{installation_id}/disable", handler.HandleDisable)
	r.Post("/installations/{installation_id}/upgrade", handler.HandleUpgrade)
	r.Delete("/installations/{installation_id}", handler.HandleUninstall)
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
	if deps.DB != nil && deps.NativeStorage != nil && deps.SecretCipher != nil && deps.RedisClient != nil {
		browser, err := nativestorage.NewSourceBrowser(deps.DB, deps.NativeStorage, deps.SecretCipher)
		if err == nil {
			browser.SetCursorStore(nativestorage.NewRedisBrowseCursorStore(deps.RedisClient))
			deps.NativeStorageManagement.Browser = browser
		}
	}
	if deps.NativeStorageManagement.S3Browse == nil {
		// One browser per composition: its rate limit and concurrency cap are
		// shared by both admin scopes.
		deps.NativeStorageManagement.S3Browse = nativestorage.NewS3Browser()
	}
	if deps.NativeStorageManagement.CatalogDiscoverLimit == nil {
		// Each catalog discovery downloads a plugin and starts a process:
		// 10 per minute per administrator, burst 3, shared by both scopes.
		deps.NativeStorageManagement.CatalogDiscoverLimit = nativestorage.NewActorRateLimiter(10, 3)
	}
	deps.NativeStorageManagement.Sources.SetInstallDiscoveryHost(deps.NativeStorage)
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
