package handlers

import (
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/google/uuid"
)

// Named wire shapes keep the management handlers and generated clients on the
// same JSON field definitions. Requests retain the strict handler validation.
type nativeStorageArtifactsResponse struct {
	Artifacts []plugins.NativeStorageArtifactView `json:"artifacts"`
}

type nativeStorageSourceResponse struct {
	Source nativestorage.SourceView `json:"source"`
}

type nativeStorageConfigurationCommand struct {
	ExpectedRevision int64                     `json:"expected_revision"`
	Config           map[string]map[string]any `json:"config"`
}

type nativeStorageConfigurationResponse struct {
	SourceKey uuid.UUID `json:"source_key"`
	Revision  int64     `json:"configuration_revision"`
}

type nativeStorageRemoveCommand struct {
	SourceKey        uuid.UUID `json:"source_key"`
	ExpectedRevision int64     `json:"expected_revision"`
}

type nativeStorageRemoveResponse struct {
	InstallationID int    `json:"installation_id"`
	State          string `json:"state"`
	Retained       bool   `json:"retained"`
}

// nativeStorageCapabilitiesResponse describes the bounded projection emitted by
// nativeStorageCapabilities; its complete shape is checked against that producer.
type nativeStorageCapabilitiesResponse struct {
	Schema                      int  `json:"schema"`
	SourceManagement            bool `json:"source_management"`
	ApprovedArtifactInstall     bool `json:"approved_artifact_install"`
	ConfigurationReplaceUnbound bool `json:"configuration_replace_unbound"`
	Disable                     bool `json:"disable"`
	Uninstall                   bool `json:"uninstall"`
	RetainedNamespaceReinstall  bool `json:"retained_namespace_reinstall"`
	Enable                      bool `json:"enable"`
	BackendVerified             bool `json:"backend_verified"`
}

// nativeStorageErrorResponse describes the fixed public fields selected by
// writeNativeStorageError. Conditional reconciliation fields remain optional.
type nativeStorageErrorResponse struct {
	Error                 string     `json:"error"`
	Message               string     `json:"message"`
	CurrentRevision       *int64     `json:"current_revision,omitempty"`
	CurrentSourceRevision *int64     `json:"current_source_revision,omitempty"`
	OperationID           *uuid.UUID `json:"operation_id,omitempty"`
	Operation             *string    `json:"operation,omitempty"`
	SourceKey             *uuid.UUID `json:"source_key,omitempty"`
}

// Request projections describe accepted wire input before the handler applies
// defaults and organization context. Domain commands are already normalized.
type nativeStorageOrganizationInstallRequest struct {
	ArtifactKey      string                    `json:"artifact_key"`
	ProviderSourceID string                    `json:"provider_source_id"`
	RootEntryID      string                    `json:"root_entry_id"`
	Enabled          bool                      `json:"enabled"`
	SourceKey        *uuid.UUID                `json:"source_key,omitempty"`
	ExpectedRevision *int64                    `json:"expected_revision,omitempty"`
	Config           map[string]map[string]any `json:"config"`
}
