package handlers

import (
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/google/uuid"
)

// Named wire shapes keep the management handlers and generated clients on the
// same JSON field definitions. Requests retain the strict handler validation.
type nativeStorageArtifactResponse struct {
	plugins.NativeStorageArtifactView
	ConfigSchema []PluginConfigSchemaView `json:"config_schema"`
}

type nativeStorageArtifactsResponse struct {
	Artifacts []nativeStorageArtifactResponse `json:"artifacts"`
}

func nativeStorageArtifactResponses(in []plugins.NativeStorageArtifactView) []nativeStorageArtifactResponse {
	out := make([]nativeStorageArtifactResponse, 0, len(in))
	for _, v := range in {
		schema := configSchemasToJSON(v.ConfigSchema)
		if schema == nil {
			schema = []PluginConfigSchemaView{}
		}
		out = append(out, nativeStorageArtifactResponse{NativeStorageArtifactView: v, ConfigSchema: schema})
	}
	return out
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
type nativeStorageSourceMedia struct {
	SourceKind string   `json:"source_kind"`
	MediaKinds []string `json:"media_kinds"`
}

type nativeStorageCapabilitiesResponse struct {
	LibraryLocationBrowsing      bool                       `json:"library_location_browsing"`
	LibraryLocationAtomicEditing bool                       `json:"library_location_atomic_editing"`
	LibraryLocationMixing        bool                       `json:"library_location_mixing"`
	SupportedSourceMedia         []nativeStorageSourceMedia `json:"supported_source_media"`
	NativeAudioLibraries         bool                       `json:"native_audio_libraries"`
	NativeAudioPlayback          bool                       `json:"native_audio_playback"`

	Schema                      int  `json:"schema"`
	SourceManagement            bool `json:"source_management"`
	ApprovedArtifactInstall     bool `json:"approved_artifact_install"`
	ConfigurationReplaceUnbound bool `json:"configuration_replace_unbound"`
	CredentialRotationInUse     bool `json:"credential_rotation_in_use"`
	Disable                     bool `json:"disable"`
	Uninstall                   bool `json:"uninstall"`
	Upgrade                     bool `json:"upgrade"`
	S3InitialRootDiscovery      bool `json:"s3_initial_root_discovery"`
	S3CatalogDiscovery          bool `json:"s3_catalog_discovery"`
	S3CheckedETagReads          bool `json:"s3_checked_etag_reads"`
	NativeMovieLibraries        bool `json:"native_movie_libraries"`
	NativeVideoPlayback         bool `json:"native_video_playback"`
	NativeVideoRemoteExecution  bool `json:"native_video_remote_execution"`
	NativeVideoDownloads        bool `json:"native_video_downloads"`
	NativeVideoJellyfin         bool `json:"native_video_jellyfin"`
	RetainedNamespaceReinstall  bool `json:"retained_namespace_reinstall"`
	Enable                      bool `json:"enable"`
	BackendVerified             bool `json:"backend_verified"`
}

// nativeStorageErrorResponse describes the fixed public fields selected by
// writeNativeStorageError. Conditional reconciliation fields remain optional.
type nativeStorageErrorResponse struct {
	Error                  string     `json:"error"`
	Message                string     `json:"message"`
	CurrentRevision        *int64     `json:"current_revision,omitempty"`
	CurrentLibraryRevision *int64     `json:"current_library_revision,omitempty"`
	LibraryID              *int       `json:"library_id,omitempty"`
	CurrentSourceRevision  *int64     `json:"current_source_revision,omitempty"`
	OperationID            *uuid.UUID `json:"operation_id,omitempty"`
	Operation              *string    `json:"operation,omitempty"`
	SourceKey              *uuid.UUID `json:"source_key,omitempty"`
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

type nativeStorageOrganizationDiscoverInstallRequest struct {
	ArtifactKey      string                    `json:"artifact_key"`
	ProviderSourceID string                    `json:"provider_source_id"`
	Enabled          bool                      `json:"enabled"`
	Config           map[string]map[string]any `json:"config"`
}

type nativeStoragePlatformDiscoverInstallRequest struct {
	nativeStorageOrganizationDiscoverInstallRequest
	OrganizationID *uuid.UUID `json:"organization_id,omitempty"`
}
