package nativestorage

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DiscoverInstallCommand is initial/new-only: the provider owns its root and
// the host generates its source key. Existing explicit-root installs retain
// their separate command, including retained-source reattachment.
type DiscoverInstallCommand struct {
	ArtifactKey      string                    `json:"artifact_key"`
	ProviderSourceID string                    `json:"provider_source_id"`
	Enabled          bool                      `json:"enabled"`
	OrganizationID   *uuid.UUID                `json:"organization_id"`
	Config           map[string]map[string]any `json:"config"`
	Binary           []byte                    `json:"-"`
}

// SetInstallDiscoveryHost is startup-only and retains the owning host's
// admission across preparation, temporary runtime reaping and publication.
func (s *SourceManagement) SetInstallDiscoveryHost(host *Host) {
	if s != nil {
		s.installDiscoveryHost = host
	}
}
func (s *SourceManagement) InitialRootDiscoveryReady() bool {
	return s != nil && s.pool != nil && s.registry != nil && s.installDiscoveryHost != nil && s.installDiscoveryHost.Registry == s.registry && s.installDiscoveryHost.Manager != nil
}

// DiscoverInstall does no provider I/O under a database connection/transaction.
// Its private manager cannot fence the shared resident manager's generation 1.
func (s *SourceManagement) DiscoverInstall(ctx context.Context, actor auth.AdminContextClaims, cmd DiscoverInstallCommand) (SourceView, error) {
	if !s.InitialRootDiscoveryReady() {
		return SourceView{}, nativeDomainError("native_storage_unavailable")
	}
	if !nativeOpaqueID(cmd.ArtifactKey) || !nativeOpaqueID(cmd.ProviderSourceID) || len(cmd.Binary) == 0 || len(cmd.Binary) > 256<<20 {
		return SourceView{}, nativeDomainError("invalid_request")
	}
	release, err := s.installDiscoveryHost.AcquireOpen()
	if err != nil {
		return SourceView{}, nativeDomainError("native_storage_unavailable")
	}
	owned := true
	defer func() {
		if owned {
			release()
		}
	}()
	tx, err := s.begin(ctx)
	if err != nil {
		return SourceView{}, err
	}
	resources := resourcetenancy.NewStore(s.pool)
	owner, err := resources.RequireStorageOwnerTx(ctx, tx, actor, cmd.OrganizationID)
	if err != nil {
		nativeDomainRollback(ctx, tx)
		return SourceView{}, nativeDomainMap(err)
	}
	if err := tx.Commit(ctx); err != nil {
		nativeDomainRollback(ctx, tx)
		return SourceView{}, nativeDomainMap(err)
	}
	// This immutable addressing copy is checked against fresh authority before
	// execution and retained again through final publication and its row waits.
	organizationID := cmd.OrganizationID
	if organizationID != nil {
		id := *organizationID
		organizationID = &id
	}
	authorize := func(ctx context.Context, tx pgx.Tx) error {
		current, err := resources.RequireStorageOwnerTx(ctx, tx, actor, organizationID)
		if err != nil {
			return nativeDomainMap(err)
		}
		if current.ID != owner.ID || current.Kind != owner.Kind || current.Revision != owner.Revision {
			return nativeDomainError("authorization_state_stale")
		}
		return nil
	}
	prepared, err := s.registry.PrepareInitialS3InstallAuthorized(ctx, plugins.NativeStorageInstallRequest{ArtifactKey: cmd.ArtifactKey, Binary: cmd.Binary, Source: storagesource.SourceConfig{Key: uuid.New(), OwnerID: owner.ID, ProviderSourceID: cmd.ProviderSourceID, Enabled: cmd.Enabled}, Config: cmd.Config}, authorize)
	if err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	discard := func() {
		if err := s.registry.DiscardInitialS3Install(prepared); err != nil {
			slog.ErrorContext(context.WithoutCancel(ctx), "native storage discovery cleanup failed", "component", "nativestorage", "error", err)
		}
	}
	snapshot, err := prepared.RuntimeSnapshot()
	if err != nil {
		discard()
		return SourceView{}, nativeDomainMap(err)
	}
	// Reservation, archive creation and filesystem work can outlive the earlier
	// check. Recheck only after the execution snapshot is ready, then release
	// the short transaction before starting any provider process or I/O.
	tx, err = s.begin(ctx)
	if err != nil {
		discard()
		return SourceView{}, nativeDomainMap(err)
	}
	if err := authorize(ctx, tx); err != nil {
		nativeDomainRollback(ctx, tx)
		discard()
		return SourceView{}, nativeDomainMap(err)
	}
	if err := tx.Commit(ctx); err != nil {
		nativeDomainRollback(ctx, tx)
		discard()
		return SourceView{}, nativeDomainMap(err)
	}
	manager := storageplugin.NewManager(storageplugin.Config{HealthInterval: time.Hour, MaxStartsPerGeneration: 1})
	discovery, cancel := context.WithTimeout(ctx, 30*time.Second)
	// Ensure detaches a successful startup from its request. This operation
	// retains cancellation through the fresh Describe and complete reaping.
	stop := context.AfterFunc(discovery, func() { manager.Disable(snapshot.Installation.ID) })
	var root string
	session, discoverErr := manager.Ensure(discovery, storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: 1, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: snapshot.Manifest, Config: snapshot.Config, Enabled: true, NativeOnly: true})
	if discoverErr == nil {
		description, err := storageplugin.Describe(discovery, session.Provider())
		discoverErr = err
		if discoverErr == nil {
			for _, source := range description.GetSources() {
				if source.GetId() == snapshot.Source.ProviderSourceID {
					root = source.GetRootEntryId()
					break
				}
			}
			discoverErr = storageplugin.ValidateSourceDescription(description, snapshot.Source.ProviderSourceID, root)
			if discoverErr == nil {
				discoverErr = s.registry.CaptureInitialS3DiscoveryState(discovery, prepared)
			}
		}
	}
	if discoverErr == nil {
		discoverErr = discovery.Err()
	}
	stop()
	cancel()
	reapCtx, reapCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	reapErr := manager.Shutdown(reapCtx)
	reapCancel()
	if reapErr != nil {
		slog.WarnContext(context.WithoutCancel(ctx), "native storage discovery reaping continues after bounded wait", "component", "nativestorage", "installation_id", snapshot.Installation.ID, "error", reapErr)
		// A bounded wait ending does not establish process exit. Transfer explicit
		// manager/package/state/admission ownership to a reaper; never publish.
		owned = false
		go func() {
			defer release()
			if err := manager.Shutdown(context.Background()); err == nil {
				discard()
			} else {
				slog.ErrorContext(context.WithoutCancel(ctx), "native storage discovery reaper failed", "component", "nativestorage", "installation_id", snapshot.Installation.ID, "error", err)
			}
		}()
		return SourceView{}, nativeDomainMap(errors.Join(discoverErr, storageplugin.ErrUnavailable, reapErr))
	}
	defer discard()
	if discoverErr != nil {
		return SourceView{}, nativeDomainError("native_storage_unavailable")
	}
	if err := ctx.Err(); err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	published, err := s.registry.PublishInitialS3InstallAuthorized(ctx, prepared, root, authorize)
	if err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	if published == nil || published.Installation == nil {
		return SourceView{}, nativeDomainError("native_storage_unavailable")
	}
	state := "attached"
	if !published.Source.Enabled || !published.Installation.Enabled {
		state = "disabled"
	}
	return sourceView(published.Source, owner, state, true), nil
}
