package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
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
