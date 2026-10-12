package api

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

// Called inside the existing signed administrative-context subtree.
func mountBloemLibraryLocations(r chi.Router, h *handlers.BloemLibraryLocationsHandler) {
	platform := h.ForAdminScope(auth.AdminScopePlatform)
	organization := h.ForAdminScope(auth.AdminScopeOrganization)
	r.Post("/platform/library-locations", platform.HandleCreate)
	r.Get("/platform/library-locations/{library_id}", platform.HandleGet)
	r.Put("/platform/library-locations/{library_id}", platform.HandleReplace)
	r.Post("/organization/library-locations", organization.HandleCreate)
	r.Get("/organization/library-locations/{library_id}", organization.HandleGet)
	r.Put("/organization/library-locations/{library_id}", organization.HandleReplace)
}

// attachNativeLibraryLocations reuses the normal library handler and the one
// browser composed with the retained host registry and existing shared Redis.
func attachNativeLibraryLocations(deps Dependencies, libraries *handlers.LibraryHandler) {
	h := deps.NativeStorageManagement
	if h == nil || libraries == nil {
		return
	}
	locations, ok := libraries.StorageLocations.(*nativestorage.LibraryLocations)
	if !ok || locations == nil {
		return
	}
	locations.SetSourceBrowser(h.Browser)
	locations.SetMixedLocationsReady(deps.LibraryIngester.MixedLocationIngestionReady())
	capabilities, ok := h.Capabilities.(interface{ LibraryLocationEditingReady(context.Context) bool })
	if !ok {
		return
	}
	h.Locations = &handlers.BloemLibraryLocationsHandler{Libraries: libraries, Locations: locations, Capabilities: capabilities}
	if c, ok := h.Capabilities.(*nativeStorageCapabilities); ok && c != nil {
		c.locations = true
	}
}
