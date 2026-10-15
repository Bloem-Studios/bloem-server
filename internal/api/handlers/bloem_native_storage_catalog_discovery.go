package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
)

// nativeStorageCatalogDiscoverRequest validates the JSON body of a catalog
// discovery. Platform and organization administrators may add an S3 source
// this way. The body never selects an owner, root, source key or revision:
// the owner is the actor's (the platform, or the actor's own organization).
func nativeStorageCatalogDiscoverRequest(cmd *nativestorage.DiscoverInstallCommand, fields map[string]json.RawMessage, scope auth.AdminScope) error {
	for field := range fields {
		switch field {
		case "artifact_key", "provider_source_id", "enabled", "config":
		default:
			// Includes organization_id, even when null, and any explicit root.
			return nativeStorageInvalid()
		}
	}
	if err := nativeStorageDiscoverInstallRequest(cmd, fields, scope); err != nil {
		return err
	}
	if _, present := cmd.Config["connection"]["state_path"]; present {
		return nativeStorageInvalid()
	}
	return nil
}

// nativeStorageCatalogDiscoverKeyAdmitted reports whether key names an
// admitted catalog release of the S3 plugin. S3 catalog records exist only for
// releases whose digest matched a static approval pin.
func nativeStorageCatalogDiscoverKeyAdmitted(artifacts []plugins.NativeStorageArtifactView, key string) bool {
	for _, a := range artifacts {
		if a.ArtifactKey == key {
			return a.Catalog && a.PluginID == nativeStorageS3PluginID
		}
	}
	return false
}

// HandleCatalogDiscoverInstall installs a catalog artifact the host downloads
// itself and discovers the new source's opaque root, as the manual discover does.
// Organization administrators create a source owned by their organization;
// SourceManagement.DiscoverInstall re-establishes that authority.
func (h *BloemNativeStorageManagementHandler) HandleCatalogDiscoverInstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var cmd nativestorage.DiscoverInstallCommand
	fields, ok := h.command(w, r, &cmd)
	if !ok {
		return
	}
	err := nativeStorageCatalogDiscoverRequest(&cmd, fields, actor.Scope)
	// Each discovery may download a plugin, resolve an endpoint and start a
	// process, so it is rate limited per administrator before any of that.
	if err == nil && h.CatalogDiscoverLimit != nil && !h.CatalogDiscoverLimit.Allow("account:"+strconv.Itoa(actor.AccountID)) {
		w.Header().Set("Retry-After", "6")
		err = nativeManagementCatalogUnavailable()
	}
	if err == nil {
		err = nativeStorageOrganizationConnectionGuard(r.Context(), actor.Scope, cmd.Config)
	}
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	if !h.sourceAvailable(w) {
		return
	}
	if h.Registry == nil {
		writeNativeStorageError(w, nativeManagementCatalogUnavailable(), true)
		return
	}
	// Refuse a key that is not an admitted (digest-pinned) S3 catalog release
	// before downloading anything. The catalog refresh is cached for a minute.
	_ = h.Registry.RefreshAvailableCatalog(r.Context())
	if !nativeStorageCatalogDiscoverKeyAdmitted(h.Registry.ApprovedArtifacts(), cmd.ArtifactKey) {
		writeNativeStorageError(w, &catalog.NativeOnboardingError{Code: "artifact_rejected"}, true)
		return
	}
	binary, err := h.Registry.CatalogBinary(r.Context(), cmd.ArtifactKey)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	cmd.Binary = binary
	source, err := h.Sources.DiscoverInstall(r.Context(), actor, cmd)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, http.StatusCreated, nativeStorageSourceResponse{source})
}
