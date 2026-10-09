package handlers

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/google/uuid"
)

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
