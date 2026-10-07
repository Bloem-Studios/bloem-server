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

type nativeStorageBindingView struct {
	BindingID uuid.UUID `json:"binding_id"`
	SourceKey uuid.UUID `json:"source_key"`
	FolderID  int       `json:"folder_id"`
}

type nativeStorageBindingsResponse struct {
	Bindings  []nativeStorageBindingView `json:"bindings"`
	NextAfter *uuid.UUID                 `json:"next_after"`
}

type nativeStorageLibraryMutationResponse struct {
	LibraryID       int       `json:"library_id"`
	CreationKey     uuid.UUID `json:"creation_key"`
	LibraryRevision int64     `json:"library_revision"`
	State           string    `json:"state"`
}

type nativeStorageLibraryResponse struct {
	Library nativestorage.LibraryStatus `json:"library"`
}

type nativeStorageInitializeCommand struct {
	ExpectedLibraryRevision int64 `json:"expected_library_revision"`
}

// nativeStorageCapabilitiesResponse describes the bounded projection emitted by
// nativeStorageCapabilities; its complete shape is checked against that producer.
type nativeStorageCapabilitiesResponse struct {
	Schema                      int             `json:"schema"`
	SourceManagement            bool            `json:"source_management"`
	ApprovedArtifactInstall     bool            `json:"approved_artifact_install"`
	ConfigurationReplaceUnbound bool            `json:"configuration_replace_unbound"`
	Disable                     bool            `json:"disable"`
	Uninstall                   bool            `json:"uninstall"`
	BindingInspection           bool            `json:"binding_inspection"`
	BindingMutation             bool            `json:"binding_mutation"`
	RetainedNamespaceReinstall  bool            `json:"retained_namespace_reinstall"`
	Enable                      bool            `json:"enable"`
	BackendVerified             bool            `json:"backend_verified"`
	SupportedOperations         map[string]bool `json:"supported_operations"`
}

// nativeStorageErrorResponse describes the fixed public fields selected by
// writeNativeStorageError. Conditional reconciliation fields remain optional.
type nativeStorageErrorResponse struct {
	Error                  string     `json:"error"`
	Message                string     `json:"message"`
	CurrentRevision        *int64     `json:"current_revision,omitempty"`
	CurrentSourceRevision  *int64     `json:"current_source_revision,omitempty"`
	CurrentLibraryRevision *int64     `json:"current_library_revision,omitempty"`
	OperationID            *uuid.UUID `json:"operation_id,omitempty"`
	Operation              *string    `json:"operation,omitempty"`
	LibraryID              *int       `json:"library_id,omitempty"`
	CreationKey            *uuid.UUID `json:"creation_key,omitempty"`
	SourceKey              *uuid.UUID `json:"source_key,omitempty"`
	ScanRunID              *string    `json:"scan_run_id,omitempty"`
	LibraryRevision        *int64     `json:"library_revision,omitempty"`
	State                  *string    `json:"state,omitempty"`
}

// Request projections describe accepted wire input before the handler applies
// defaults and organization context. Domain commands are already normalized.
type nativeStorageLibraryCreateRequest struct {
	Name             string     `json:"name"`
	MetadataLanguage *string    `json:"metadata_language,omitempty"`
	OrganizationID   *uuid.UUID `json:"organization_id,omitempty"`
}
type nativeStorageOrganizationLibraryCreateRequest struct {
	Name             string  `json:"name"`
	MetadataLanguage *string `json:"metadata_language,omitempty"`
}
type nativeStorageOrganizationInstallRequest struct {
	ArtifactKey      string                    `json:"artifact_key"`
	ProviderSourceID string                    `json:"provider_source_id"`
	RootEntryID      string                    `json:"root_entry_id"`
	Enabled          bool                      `json:"enabled"`
	SourceKey        *uuid.UUID                `json:"source_key,omitempty"`
	ExpectedRevision *int64                    `json:"expected_revision,omitempty"`
	Config           map[string]map[string]any `json:"config"`
}
