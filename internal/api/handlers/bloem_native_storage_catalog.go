package handlers

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/google/uuid"
)

const nativeStorageS3PluginID = "bloem.storage.s3"

// nativeStorageCatalogPluginID names the plugin an artifact key installs: an
// approved artifact's own plugin, else the catalog key prefix, so a key the
// catalog has not been fetched for yet is still recognised. Unknown keys
// return "".
func nativeStorageCatalogPluginID(artifacts []plugins.NativeStorageArtifactView, key string) string {
	for _, a := range artifacts {
		if a.ArtifactKey == key {
			return a.PluginID
		}
	}
	id, _ := plugins.NativeCatalogArtifactPluginID(key)
	return id
}

// nativeStorageCatalogKeyAllowed decides, before any download, whether a
// catalog install or upgrade may use a plugin. S3 is platform-only: an
// organization administrator gets not_found, and a platform administrator
// must add an S3 source through catalog discovery, which discovers its root,
// so a plain catalog install of S3 is artifact_rejected. Other plugins keep
// their existing checks.
func nativeStorageCatalogKeyAllowed(pluginID string, scope auth.AdminScope, install bool) error {
	if pluginID != nativeStorageS3PluginID {
		return nil
	}
	if scope != auth.AdminScopePlatform {
		return &catalog.NativeOnboardingError{Code: "not_found"}
	}
	if install {
		return &catalog.NativeOnboardingError{Code: "artifact_rejected"}
	}
	return nil
}

// Catalog downloads are fixed publisher URLs, never request-provided URLs.
// SourceManagement retains source and admin authority in its write transaction.
func (h *BloemNativeStorageManagementHandler) HandleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var cmd nativestorage.InstallCommand
	fields, ok := h.command(w, r, &cmd)
	if !ok {
		return
	}
	if err := nativeStorageInstallRequest(&cmd, fields, actor.Scope); err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	if err := nativeStorageCatalogKeyAllowed(nativeStorageCatalogPluginID(h.Registry.ApprovedArtifacts(), cmd.ArtifactKey), actor.Scope, true); err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	if err := nativeStorageOrganizationConnectionGuard(r.Context(), actor.Scope, cmd.Config); err != nil {
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
	source, err := h.Sources.Install(r.Context(), actor, cmd)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, 201, nativeStorageSourceResponse{source})
}
func (h *BloemNativeStorageManagementHandler) HandleCatalogUpgrade(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := nativeStoragePathID(w, r, "installation_id")
	if !ok {
		return
	}
	var cmd nativestorage.UpgradeCommand
	_, ok = h.command(w, r, &cmd)
	if !ok {
		return
	}
	if !nativeStorageText(cmd.ArtifactKey, 1024) || cmd.ExpectedRevision <= 0 || cmd.SourceKey == uuid.Nil {
		writeNativeStorageError(w, nativeStorageInvalid(), true)
		return
	}
	if err := nativeStorageCatalogKeyAllowed(nativeStorageCatalogPluginID(h.Registry.ApprovedArtifacts(), cmd.ArtifactKey), actor.Scope, false); err != nil {
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
	source, err := h.Sources.Upgrade(r.Context(), actor, id, cmd)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, 200, nativeStorageSourceResponse{source})
}

func nativeManagementCatalogUnavailable() error {
	return &catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode}
}
