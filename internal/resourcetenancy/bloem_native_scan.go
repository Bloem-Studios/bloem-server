package resourcetenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RequireNativeScan authorizes a host background scan of an explicit durable
// native binding. It establishes no account membership or end-user access.
// Its short transaction must finish before provider I/O starts.
func (s *Store) RequireNativeScan(ctx context.Context, binding storagesource.Binding, expected storagesource.SourceConfig) error {
	if s == nil || s.pool == nil {
		return ErrResourceUnavailable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nativeScanError(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = s.RequireNativeScanTx(ctx, tx, binding, expected); err != nil {
		return err
	}
	return nativeScanError(tx.Commit(ctx))
}

// RequireNativeScanTx retains authorization locks through the caller's commit.
// Call it BEFORE acquiring source/run/ingestion locks: registry mutations lock
// installation -> source -> runs, and uninstall then deletes entitlements.
// The caller must use the same binding/source as its ingestion claim and must
// not perform network I/O or commit inside an authorization/publication callback.
func (s *Store) RequireNativeScanTx(ctx context.Context, tx pgx.Tx, binding storagesource.Binding, expected storagesource.SourceConfig) error {
	if s == nil || s.pool == nil || tx == nil {
		return ErrResourceUnavailable
	}
	if binding.ID == uuid.Nil || binding.SourceKey == uuid.Nil || binding.FolderID <= 0 ||
		expected.Key != binding.SourceKey || expected.OwnerID == uuid.Nil ||
		expected.InstallationID == nil || *expected.InstallationID <= 0 ||
		!expected.Enabled || expected.ConfigurationRevision <= 0 {
		return ErrResourceHidden
	}

	// SHARE conflicts with registry NO KEY UPDATE; KEY SHARE would not. Inspect
	// the actual installation before any source lock, including after lock waits.
	var installationOwner uuid.UUID
	var pluginID, kind string
	var enabled bool
	err := tx.QueryRow(ctx, "SELECT owner_id,plugin_id,kind,enabled FROM plugin_installations WHERE id=$1 FOR SHARE", *expected.InstallationID).Scan(&installationOwner, &pluginID, &kind, &enabled)
	if err != nil {
		return nativeScanError(err)
	}
	if installationOwner != expected.OwnerID || pluginID != expected.PluginID || kind != "plugin" || !enabled {
		return ErrResourceHidden
	}
	var markerOwner uuid.UUID
	var protocol int
	err = tx.QueryRow(ctx, "SELECT owner_id,protocol_version FROM bloem_storage_installations WHERE installation_id=$1 FOR SHARE", *expected.InstallationID).Scan(&markerOwner, &protocol)
	if err != nil {
		return nativeScanError(err)
	}
	if markerOwner != installationOwner || protocol != 1 {
		return ErrResourceHidden
	}

	// Reload every retained field under the source lock; config/owner equality
	// from a cached registry snapshot alone cannot authorize publication.
	var actual storagesource.SourceConfig
	err = tx.QueryRow(ctx, "SELECT key,owner_id,installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled FROM bloem_storage_sources WHERE key=$1 FOR UPDATE", expected.Key).Scan(&actual.Key, &actual.OwnerID, &actual.InstallationID, &actual.PluginID, &actual.ProviderSourceID, &actual.RootEntryID, &actual.ConfigurationRevision, &actual.Enabled)
	if err != nil {
		return nativeScanError(err)
	}
	if actual.Key != expected.Key || actual.OwnerID != expected.OwnerID ||
		actual.InstallationID == nil || *actual.InstallationID != *expected.InstallationID ||
		actual.PluginID != expected.PluginID || actual.ProviderSourceID != expected.ProviderSourceID ||
		actual.RootEntryID != expected.RootEntryID || actual.ConfigurationRevision != expected.ConfigurationRevision ||
		actual.Enabled != expected.Enabled {
		return ErrResourceHidden
	}

	// Folder UPDATE excludes type/enabled/owner changes and new paths/bindings
	// whose foreign keys take KEY SHARE. Lock the folder before binding/path rows
	// to agree with parent deletion. Existing blank paths also need row locks.
	// NOWAIT fails closed if a paths-only replacement already locked a child
	// before attempting its parent FK, avoiding a child/parent lock inversion.
	var folderOwner uuid.UUID
	err = tx.QueryRow(ctx, "SELECT owner_id,type,enabled FROM media_folders WHERE id=$1 FOR UPDATE", binding.FolderID).Scan(&folderOwner, &kind, &enabled)
	if err != nil {
		return nativeScanError(err)
	}
	if !librarykind.IsEbook(kind) || !enabled {
		return ErrResourceHidden
	}
	var actualBinding storagesource.Binding
	err = tx.QueryRow(ctx, "SELECT id,source_key,folder_id FROM bloem_storage_bindings WHERE id=$1 FOR SHARE", binding.ID).Scan(&actualBinding.ID, &actualBinding.SourceKey, &actualBinding.FolderID)
	if err != nil {
		return nativeScanError(err)
	}
	if actualBinding != binding {
		return ErrResourceHidden
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM (SELECT 1 FROM bloem_storage_bindings WHERE folder_id=$1 LIMIT 2) AS bindings", binding.FolderID).Scan(&count); err != nil {
		return nativeScanError(err)
	}
	if count != 1 {
		return ErrResourceHidden
	}
	paths, err := tx.Query(ctx, "SELECT path FROM media_folder_paths WHERE media_folder_id=$1 ORDER BY id FOR SHARE NOWAIT", binding.FolderID)
	if err != nil {
		return nativeScanError(err)
	}
	for paths.Next() {
		var path string
		if err = paths.Scan(&path); err != nil {
			paths.Close()
			return nativeScanError(err)
		}
		if strings.TrimSpace(path) != "" {
			paths.Close()
			return ErrResourceHidden
		}
	}
	err = paths.Err()
	paths.Close()
	if err != nil {
		return nativeScanError(err)
	}

	// Lock both typed owners in UUID order. Owner identity is resource authority,
	// not evidence of account membership; only the explicit system policy below
	// permits a host scan of these actual active resources.
	owners, err := tx.Query(ctx, "SELECT id,kind,organization_id,revision FROM resource_owners WHERE id=$1 OR id=$2 ORDER BY id FOR SHARE", folderOwner, installationOwner)
	if err != nil {
		return nativeScanError(err)
	}
	byID := make(map[uuid.UUID]Owner, 2)
	for owners.Next() {
		var owner Owner
		if err = owners.Scan(&owner.ID, &owner.Kind, &owner.OrganizationID, &owner.Revision); err != nil {
			owners.Close()
			return nativeScanError(err)
		}
		byID[owner.ID] = owner
	}
	err = owners.Err()
	owners.Close()
	if err != nil {
		return nativeScanError(err)
	}
	folder, folderOK := byID[folderOwner]
	source, sourceOK := byID[installationOwner]
	if !folderOK || !sourceOK || !nativeScanOwnerValid(folder) || !nativeScanOwnerValid(source) {
		return ErrResourceHidden
	}
	if folder.Kind == OwnerPlatform {
		if source.Kind != OwnerPlatform {
			return ErrResourceHidden
		}
		return nil
	}
	if source.Kind == OwnerOrganization && *source.OrganizationID != *folder.OrganizationID {
		return ErrResourceHidden
	}

	var status string
	err = tx.QueryRow(ctx, "SELECT status FROM organizations WHERE id=$1 FOR SHARE", *folder.OrganizationID).Scan(&status)
	if err != nil {
		return nativeScanError(err)
	}
	if status != "active" {
		return ErrResourceHidden
	}
	if source.Kind == OwnerOrganization {
		return nil
	}
	var entitlementID uuid.UUID
	err = tx.QueryRow(ctx, "SELECT id FROM organization_entitlements WHERE organization_id=$1 AND root_owner_id=$2 AND root_kind='plugin_installation' AND entitlement_kind='plugin_availability' AND plugin_installation_id=$3 AND status='active' FOR SHARE", *folder.OrganizationID, installationOwner, *actual.InstallationID).Scan(&entitlementID)
	return nativeScanError(err)
}

func nativeScanOwnerValid(owner Owner) bool {
	return owner.ID != uuid.Nil && owner.Revision > 0 &&
		((owner.Kind == OwnerPlatform && owner.OrganizationID == nil) ||
			(owner.Kind == OwnerOrganization && owner.OrganizationID != nil && *owner.OrganizationID != uuid.Nil))
}
func nativeScanError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrResourceHidden
	}
	return fmt.Errorf("%w: native scan authority: %w", ErrResourceUnavailable, err)
}
