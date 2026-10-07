package nativestorage

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MutationOutcomeUnknown = catalog.MutationOutcomeUnknown

type SourceView struct {
	SourceKey             uuid.UUID                 `json:"source_key"`
	OwnerKind             resourcetenancy.OwnerKind `json:"owner_kind"`
	OrganizationID        *uuid.UUID                `json:"organization_id"`
	InstallationID        *int64                    `json:"installation_id"`
	PluginID              string                    `json:"plugin_id"`
	ProviderSourceID      string                    `json:"provider_source_id"`
	RootEntryID           string                    `json:"root_entry_id"`
	ConfigurationRevision int64                     `json:"configuration_revision"`
	Enabled               bool                      `json:"enabled"`
	State                 string                    `json:"state"`
	Configured            bool                      `json:"configured"`
}
type SourcePage struct {
	Sources   []SourceView `json:"sources"`
	NextAfter *uuid.UUID   `json:"next_after"`
}
type InstallCommand struct {
	ArtifactKey      string                    `json:"artifact_key"`
	ProviderSourceID string                    `json:"provider_source_id"`
	RootEntryID      string                    `json:"root_entry_id"`
	Enabled          bool                      `json:"enabled"`
	SourceKey        *uuid.UUID                `json:"source_key"`
	OrganizationID   *uuid.UUID                `json:"organization_id"`
	ExpectedRevision *int64                    `json:"expected_revision"`
	Config           map[string]map[string]any `json:"config"`
	Binary           []byte                    `json:"-"`
}
type SourceManagement struct {
	pool        *pgxpool.Pool
	registry    *plugins.NativeStorageRegistry
	coordinator *Coordinator
}

func NewSourceManagement(pool *pgxpool.Pool, registry *plugins.NativeStorageRegistry) *SourceManagement {
	return &SourceManagement{pool: pool, registry: registry}
}

// SetCoordinator is startup-only. C attaches the actual reader's instance.
func (s *SourceManagement) SetCoordinator(c *Coordinator) {
	if s != nil {
		s.coordinator = c
	}
}
func nativeDomainError(code string) error { return &catalog.NativeOnboardingError{Code: code} }
func nativeDomainMap(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, resourcetenancy.ErrResourceHidden) || errors.Is(err, storagesource.ErrSourceUnavailable) || errors.Is(err, pgx.ErrNoRows) || errors.Is(err, catalog.ErrFolderNotFound) {
		return nativeDomainError("not_found")
	}
	if errors.Is(err, resourcetenancy.ErrInvalidActor) {
		return nativeDomainError("authorization_state_stale")
	}
	return catalog.MapNativeOnboardingError(err)
}
func nativeOpaqueID(value string) bool {
	return value != "" && len(value) <= 1024 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
func nativePageLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 100 {
		return 0, nativeDomainError("invalid_request")
	}
	return limit, nil
}
func (s *SourceManagement) begin(ctx context.Context) (pgx.Tx, error) {
	if s == nil || s.pool == nil || s.registry == nil {
		return nil, nativeDomainError("native_storage_unavailable")
	}
	return nativeDomainBegin(ctx, s.pool)
}
func (s *SourceManagement) Install(ctx context.Context, actor auth.AdminContextClaims, cmd InstallCommand) (SourceView, error) {
	if !nativeOpaqueID(cmd.ArtifactKey) || !nativeOpaqueID(cmd.ProviderSourceID) || !nativeOpaqueID(cmd.RootEntryID) || len(cmd.Binary) == 0 || len(cmd.Binary) > 256<<20 ||
		(cmd.SourceKey == nil && cmd.ExpectedRevision != nil) || (cmd.SourceKey != nil && (*cmd.SourceKey == uuid.Nil || cmd.ExpectedRevision == nil || *cmd.ExpectedRevision <= 0)) {
		return SourceView{}, nativeDomainError("invalid_request")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return SourceView{}, err
	}
	defer nativeDomainRollback(ctx, tx)
	resources := resourcetenancy.NewStore(s.pool)
	owner, err := resources.RequireStorageOwnerTx(ctx, tx, actor, cmd.OrganizationID)
	if err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	key := uuid.New()
	if cmd.SourceKey != nil {
		key = *cmd.SourceKey
	}
	pluginID := ""
	for _, artifact := range s.registry.ApprovedArtifacts() {
		if artifact.ArtifactKey == cmd.ArtifactKey {
			pluginID = artifact.PluginID
			break
		}
	}
	if pluginID == "" {
		return SourceView{}, nativeDomainError("artifact_rejected")
	}
	// Preflight real ownership/revision before package work. The same closure runs
	// again inside the registry's transaction, after its relevant row waits.
	authorize := func(ctx context.Context, tx pgx.Tx) error {
		if cmd.SourceKey == nil {
			currentOwner, e := resources.RequireStorageOwnerTx(ctx, tx, actor, cmd.OrganizationID)
			if e != nil {
				return nativeDomainMap(e)
			}
			if currentOwner.ID != owner.ID {
				return nativeDomainError("not_found")
			}
		}
		_, e := nativeDomainSourceTx(ctx, tx, key)
		if cmd.SourceKey == nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return nil
			}
			if e != nil {
				return nativeDomainMap(e)
			}
			return nativeDomainError("source_attached")
		}
		if e != nil {
			return nativeDomainMap(e)
		}
		if e = resources.RequireNativeManagementTx(ctx, tx, actor, key, nil, owner.ID, true); e != nil {
			return nativeDomainMap(e)
		}
		source, e := nativeDomainSourceTx(ctx, tx, key)
		if e != nil {
			return nativeDomainMap(e)
		}
		if e = nativeSourceRevision(source.ConfigurationRevision, *cmd.ExpectedRevision); e != nil {
			return e
		}
		if source.InstallationID != nil || source.Enabled {
			return nativeDomainError("source_attached")
		}
		if source.OwnerID != owner.ID || source.PluginID != pluginID || source.ProviderSourceID != cmd.ProviderSourceID || source.RootEntryID != cmd.RootEntryID {
			return nativeDomainError("retained_namespace_unverified")
		}
		return nil
	}
	if err = authorize(ctx, tx); err != nil {
		return SourceView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	snapshot, err := s.registry.InstallAuthorized(ctx, plugins.NativeStorageInstallRequest{ArtifactKey: cmd.ArtifactKey, Binary: cmd.Binary,
		Source: storagesource.SourceConfig{Key: key, OwnerID: owner.ID, PluginID: pluginID, ProviderSourceID: cmd.ProviderSourceID, RootEntryID: cmd.RootEntryID, Enabled: cmd.Enabled}, Config: cmd.Config}, authorize)
	if err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	if snapshot == nil || snapshot.Installation == nil {
		return SourceView{}, nativeDomainError("native_storage_unavailable")
	}
	state := "attached"
	if !snapshot.Source.Enabled || !snapshot.Installation.Enabled {
		state = "disabled"
	}
	return sourceView(snapshot.Source, owner, state, len(cmd.Config) > 0), nil
}
func (s *SourceManagement) ReplaceConfiguration(ctx context.Context, actor auth.AdminContextClaims, key uuid.UUID, expected int64, config map[string]map[string]any) (int64, error) {
	if key == uuid.Nil || expected <= 0 {
		return 0, nativeDomainError("invalid_request")
	}
	source, err := s.preflightMutation(ctx, actor, key, nil, expected)
	if err != nil {
		return 0, err
	}
	if source.InstallationID == nil {
		return 0, nativeDomainError("native_storage_unavailable")
	}
	if s.coordinator == nil || s.coordinator.Runtime == nil {
		return 0, nativeDomainError("native_storage_unavailable")
	}
	installationID := *source.InstallationID
	authorize := s.sourceMutationAuthorizer(actor, source, &installationID, expected)
	var revision int64
	err = s.coordinator.FenceMutation(ctx, int(installationID), func(ctx context.Context) error {
		var e error
		revision, e = s.registry.ReplaceConfigurationAuthorized(ctx, key, source.OwnerID, expected, config, authorize)
		return e
	})
	return revision, nativeDomainMap(err)
}
func (s *SourceManagement) Remove(ctx context.Context, actor auth.AdminContextClaims, installationID int, key uuid.UUID, expected int64, uninstall bool) error {
	if installationID <= 0 || key == uuid.Nil || expected <= 0 {
		return nativeDomainError("invalid_request")
	}
	id := int64(installationID)
	source, err := s.preflightMutation(ctx, actor, key, &id, expected)
	if err != nil {
		return err
	}
	if s.coordinator == nil || s.coordinator.Runtime == nil {
		return nativeDomainError("native_storage_unavailable")
	}
	authorize := s.sourceMutationAuthorizer(actor, source, &id, expected)
	return nativeDomainMap(s.coordinator.FenceMutation(ctx, installationID, func(ctx context.Context) error {
		return s.registry.RemoveAuthorized(ctx, installationID, key, source.OwnerID, expected, uninstall, authorize)
	}))
}

// UpgradeCommand installs a newer approved artifact on a source's installation.
type UpgradeCommand struct {
	ArtifactKey      string    `json:"artifact_key"`
	SourceKey        uuid.UUID `json:"source_key"`
	ExpectedRevision int64     `json:"expected_revision"`
	Binary           []byte    `json:"-"`
}

// Upgrade replaces a source's plugin executable with a newer approved artifact
// of the same plugin. Its configuration, library location and catalog are
// kept; open files and scans on the old process end, and the next scan and
// reads use the new one.
func (s *SourceManagement) Upgrade(ctx context.Context, actor auth.AdminContextClaims, installationID int, cmd UpgradeCommand) (SourceView, error) {
	if installationID <= 0 || cmd.SourceKey == uuid.Nil || cmd.ExpectedRevision <= 0 || !nativeOpaqueID(cmd.ArtifactKey) || len(cmd.Binary) == 0 || len(cmd.Binary) > 256<<20 {
		return SourceView{}, nativeDomainError("invalid_request")
	}
	id := int64(installationID)
	source, err := s.preflightMutation(ctx, actor, cmd.SourceKey, &id, cmd.ExpectedRevision)
	if err != nil {
		return SourceView{}, err
	}
	if s.coordinator == nil || s.coordinator.Runtime == nil {
		return SourceView{}, nativeDomainError("native_storage_unavailable")
	}
	authorize := s.sourceMutationAuthorizer(actor, source, &id, cmd.ExpectedRevision)
	err = s.coordinator.FenceMutation(ctx, installationID, func(ctx context.Context) error {
		_, e := s.registry.UpgradeAuthorized(ctx, installationID, cmd.SourceKey, source.OwnerID, cmd.ArtifactKey, cmd.Binary, authorize)
		return e
	})
	if err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	return s.GetSource(ctx, actor, cmd.SourceKey)
}

func (s *SourceManagement) preflightMutation(ctx context.Context, actor auth.AdminContextClaims, key uuid.UUID, installationID *int64, expected int64) (storagesource.SourceConfig, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return storagesource.SourceConfig{}, err
	}
	defer nativeDomainRollback(ctx, tx)
	source, err := nativeDomainSourceTx(ctx, tx, key)
	if err != nil {
		return source, nativeDomainMap(err)
	}
	authorize := s.sourceMutationAuthorizer(actor, source, installationID, expected)
	if err = authorize(ctx, tx); err != nil {
		return source, err
	}
	source, err = nativeDomainSourceTx(ctx, tx, key)
	if err != nil {
		return source, nativeDomainMap(err)
	}
	return source, nativeDomainMap(tx.Commit(ctx))
}
func (s *SourceManagement) sourceMutationAuthorizer(actor auth.AdminContextClaims, source storagesource.SourceConfig, installationID *int64, expected int64) plugins.NativeStorageAuthorizeTx {
	return func(ctx context.Context, tx pgx.Tx) error {
		resources := resourcetenancy.NewStore(s.pool)
		if err := resources.RequireNativeManagementTx(ctx, tx, actor, source.Key, installationID, source.OwnerID, true); err != nil {
			return nativeDomainMap(err)
		}
		current, err := nativeDomainSourceTx(ctx, tx, source.Key)
		if err != nil {
			return nativeDomainMap(err)
		}
		return nativeSourceRevision(current.ConfigurationRevision, expected)
	}
}
