package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/google/uuid"
)

func nativeStorageDiscoverInstallBody(w http.ResponseWriter, r *http.Request) (nativestorage.DiscoverInstallCommand, map[string]json.RawMessage, error) {
	request, binary, err := nativeStorageMultipart(w, r)
	if err != nil {
		return nativestorage.DiscoverInstallCommand{}, nil, err
	}
	var cmd nativestorage.DiscoverInstallCommand
	fields, err := nativeStorageDecodeJSON(request, &cmd)
	if err != nil {
		return nativestorage.DiscoverInstallCommand{}, nil, err
	}
	cmd.Binary = binary
	return cmd, fields, nil
}

func nativeStorageDiscoverInstallRequest(cmd *nativestorage.DiscoverInstallCommand, fields map[string]json.RawMessage, scope auth.AdminScope) error {
	if scope == auth.AdminScopeOrganization {
		if _, present := fields["organization_id"]; present {
			return nativeStorageInvalid()
		}
	}
	for _, field := range []string{"artifact_key", "provider_source_id", "enabled", "config"} {
		if raw, present := fields[field]; !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nativeStorageInvalid()
		}
	}
	if !nativeStorageText(cmd.ArtifactKey, 1024) || !nativeStorageText(cmd.ProviderSourceID, 1024) ||
		(cmd.OrganizationID != nil && *cmd.OrganizationID == uuid.Nil) {
		return nativeStorageInvalid()
	}
	return nativeStorageConfiguration(cmd.Config)
}

// HandleDiscoverInstall discovers the exact opaque root of a new, approved S3
// installation. The service owns approval, current authority and publication.
func (h *BloemNativeStorageManagementHandler) HandleDiscoverInstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	if _, err := nativeStorageQuery(r); err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	cmd, fields, err := nativeStorageDiscoverInstallBody(w, r)
	if err == nil {
		err = nativeStorageDiscoverInstallRequest(&cmd, fields, actor.Scope)
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
	source, err := h.Sources.DiscoverInstall(r.Context(), actor, cmd)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, http.StatusCreated, nativeStorageSourceResponse{source})
}
