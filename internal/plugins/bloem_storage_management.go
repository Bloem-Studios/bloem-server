package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// NativeStorageAuthorizeTx retains real authority in the registry write transaction.
// It may not commit, do provider I/O, or derive a grant from a source UUID.
type NativeStorageAuthorizeTx func(context.Context, pgx.Tx) error

type NativeStorageArtifactView struct {
	ArtifactKey string `json:"artifact_key"`
	PluginID    string `json:"plugin_id"`
	Version     string `json:"version"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
}

func (r *NativeStorageRegistry) ApprovedArtifacts() []NativeStorageArtifactView {
	result := make([]NativeStorageArtifactView, 0)
	if r == nil {
		return result
	}
	for key, a := range r.artifactSnapshot() {
		result = append(result, NativeStorageArtifactView{key, a.Manifest.GetPluginId(), a.Manifest.GetVersion(), a.OS, a.Arch})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ArtifactKey < result[j].ArtifactKey })
	return result
}
func nativeManagementUnavailable() error {
	return &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
}
func (r *NativeStorageRegistry) InstallAuthorized(ctx context.Context, req NativeStorageInstallRequest, authorize NativeStorageAuthorizeTx) (*NativeStorageSnapshot, error) {
	if r == nil || r.pool == nil || authorize == nil {
		return nil, nativeManagementUnavailable()
	}
	a, ok := r.artifact(req.ArtifactKey)
	if !ok {
		return nil, &catalog.NativeOnboardingError{Code: "artifact_rejected"}
	}
	if err := validateNativeManagementConfigForOperation(a.Manifest, req.Config, "artifact_rejected"); err != nil {
		return nil, err
	}
	return r.install(ctx, req, authorize)
}
func (r *NativeStorageRegistry) ReplaceConfigurationAuthorized(ctx context.Context, key, owner uuid.UUID, expected int64, config map[string]map[string]any, authorize NativeStorageAuthorizeTx) (int64, error) {
	if r == nil || r.pool == nil || authorize == nil {
		return 0, nativeManagementUnavailable()
	}
	if key == uuid.Nil || owner == uuid.Nil || expected <= 0 {
		return 0, &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	return r.replaceConfiguration(ctx, key, owner, config, &expected, authorize)
}
func (r *NativeStorageRegistry) RemoveAuthorized(ctx context.Context, id int, key, owner uuid.UUID, expected int64, uninstall bool, authorize NativeStorageAuthorizeTx) error {
	if r == nil || r.pool == nil || authorize == nil {
		return nativeManagementUnavailable()
	}
	if id <= 0 || key == uuid.Nil || owner == uuid.Nil || expected <= 0 {
		return &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	return r.removeGuarded(ctx, id, owner, uninstall, &key, &expected, authorize)
}
func validateNativeManagementConfig(m *publicv1.PluginManifest, config map[string]map[string]any) error {
	return validateNativeManagementConfigForOperation(m, config, "invalid_request")
}
func validateNativeManagementConfigForOperation(m *publicv1.PluginManifest, config map[string]map[string]any, schemaCode string) error {
	invalid := func() error { return &catalog.NativeOnboardingError{Code: "invalid_request"} }
	rejected := func() error { return &catalog.NativeOnboardingError{Code: schemaCode} }
	// Host shape and serialized bounds are independent of the plugin schema.
	// Validate them first so a schema refusal cannot conceal either category.
	entries := make([]*publicv1.ConfigEntry, 0, len(config))
	for key, value := range config {
		if !nativeStorageText(key) {
			return invalid()
		}
		if value == nil {
			value = map[string]any{}
		}
		v, err := structpb.NewStruct(value)
		if err != nil {
			return invalid()
		}
		entries = append(entries, &publicv1.ConfigEntry{Key: key, Value: v})
	}
	if proto.Size(&publicv1.ConfigureRequest{Config: entries}) > 1<<20 {
		return &catalog.NativeOnboardingError{Code: "request_too_large"}
	}
	if m == nil {
		return rejected()
	}
	declared := make(map[string]bool)
	for _, schema := range m.GetGlobalConfigSchema() {
		if schema == nil || !nativeStorageText(schema.GetKey()) || declared[schema.GetKey()] {
			return rejected()
		}
		declared[schema.GetKey()] = true
		if schema.GetRequired() {
			if _, ok := config[schema.GetKey()]; !ok {
				return rejected()
			}
		}
	}
	for key, value := range config {
		if !declared[key] {
			return rejected()
		}
		if value == nil {
			value = map[string]any{}
		}
		if err := ValidateGlobalConfigValue(m, key, value); err != nil {
			return rejected()
		}
	}
	return nil
}

// Any library location or discovered namespace makes replacing credentials/reattaching
// unsafe, including evidence belonging to a sibling source.
func nativeManagementEmptyNamespaceTx(ctx context.Context, tx pgx.Tx, keys []uuid.UUID, refusalCode string) error {
	var populated bool
	err := tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM library_storage_locations WHERE source_key=ANY($1::uuid[])) OR
 EXISTS(SELECT 1 FROM bloem_storage_entries WHERE source_key=ANY($1::uuid[])) OR
 EXISTS(SELECT 1 FROM bloem_storage_file_refs r JOIN library_storage_locations l ON l.id=r.location_id WHERE l.source_key=ANY($1::uuid[]))`, keys).Scan(&populated)
	if err != nil {
		return catalog.MapNativeOnboardingError(err)
	}
	if populated {
		return &catalog.NativeOnboardingError{Code: refusalCode}
	}
	return nil
}
func nativeManagementRevision(actual, expected int64) error {
	if actual == expected {
		return nil
	}
	return &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentSourceRevision: &actual}
}
func nativeManagementCommitError(err error, operation string, key uuid.UUID, operationID uuid.UUID) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return catalog.MapNativeOnboardingError(err)
	}
	return &catalog.MutationOutcomeUnknown{OperationID: operationID, SourceKey: &key, Operation: operation, Cause: err}
}

// A per-call preparation is evidence about bytes, never retained authority.
// Its transaction is always rolled back before ZIP/schema validation: the
// authorizer may have transaction-local effects that must not be published.
type nativeManagementConfigurationArtifact struct {
	source       storagesource.SourceConfig
	installation *Installation
	archive      *InstallationArchive
	fingerprint  [sha256.Size]byte
}

func (r *NativeStorageRegistry) prepareManagementConfigurationArtifact(ctx context.Context, key, owner uuid.UUID, expected int64, authorize NativeStorageAuthorizeTx) (prepared *nativeManagementConfigurationArtifact, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackCtx); rollbackErr != nil {
			prepared = nil
			err = errors.Join(err, rollbackErr)
		}
	}()
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='10s'"); err != nil {
		return nil, err
	}
	if err = authorize(ctx, tx); err != nil {
		return nil, err
	}
	source, err := nativeStorageSource(ctx, tx, key, owner, false)
	if err != nil {
		return nil, err
	}
	if source.InstallationID == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	installation, err := r.checkInstallation(ctx, tx, int(*source.InstallationID), owner, true)
	if err != nil {
		return nil, err
	}
	source, err = nativeStorageSource(ctx, tx, key, owner, true)
	if err != nil {
		return nil, err
	}
	if !source.Enabled || !installation.Enabled || source.InstallationID == nil || *source.InstallationID != int64(installation.ID) || source.PluginID != installation.PluginID {
		return nil, storagesource.ErrSourceUnavailable
	}
	if err = authorize(ctx, tx); err != nil {
		return nil, err
	}
	if err = nativeManagementRevision(source.ConfigurationRevision, expected); err != nil {
		return nil, err
	}
	archive, err := scanArchive(tx.QueryRow(ctx, `SELECT `+archiveColumns+` FROM plugin_archives WHERE plugin_installation_id=$1`, installation.ID))
	if err != nil {
		return nil, catalog.MapNativeOnboardingError(err)
	}
	return &nativeManagementConfigurationArtifact{source: source, installation: installation, archive: archive}, nil
}

func (p *nativeManagementConfigurationArtifact) matches(source storagesource.SourceConfig, installation *Installation, archive *InstallationArchive) bool {
	return source.InstallationID != nil && p.source.InstallationID != nil &&
		source.Key == p.source.Key && source.OwnerID == p.source.OwnerID &&
		*source.InstallationID == *p.source.InstallationID && source.PluginID == p.source.PluginID &&
		source.ProviderSourceID == p.source.ProviderSourceID && source.RootEntryID == p.source.RootEntryID &&
		source.ConfigurationRevision == p.source.ConfigurationRevision && source.Enabled == p.source.Enabled &&
		installation.ID == p.installation.ID && installation.PluginID == p.installation.PluginID &&
		installation.Version == p.installation.Version && installation.InstallPath == p.installation.InstallPath &&
		installation.Enabled == p.installation.Enabled && installation.Kind == p.installation.Kind &&
		installation.RuntimeGeneration == p.installation.RuntimeGeneration &&
		archive.InstallationID == p.archive.InstallationID && archive.Checksum == p.archive.Checksum &&
		bytes.Equal(archive.ManifestJSON, p.archive.ManifestJSON) && sha256.Sum256(archive.Bytes) == p.fingerprint
}

func (r *NativeStorageRegistry) managementManifestArchive(archive *InstallationArchive) (*publicv1.PluginManifest, error) {
	var installed publicv1.PluginManifest
	if err := protojson.Unmarshal(archive.ManifestJSON, &installed); err != nil {
		return nil, nativeManagementUnavailable()
	}
	for _, a := range r.artifactSnapshot() {
		if a.Checksum == archive.Checksum && proto.Equal(a.Manifest, &installed) {
			// This expensive validation runs only after the preparation rolled back.
			if err := validateNativeStorageArchive(archive.Bytes, a.Manifest, a.Checksum); err != nil {
				return nil, nativeManagementUnavailable()
			}
			return a.Manifest, nil
		}
	}
	return nil, storagesource.ErrSourceUnavailable
}
