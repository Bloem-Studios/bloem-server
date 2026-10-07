package nativestorage

import (
	"context"
	"errors"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeDomainBegin(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	if pool == nil {
		return nil, nativeDomainError("native_storage_unavailable")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, nativeDomainMap(err)
	}
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='10s'"); err != nil {
		nativeDomainRollback(ctx, tx)
		return nil, nativeDomainMap(err)
	}
	if !catalog.NativeStorageSchemaReady(ctx, tx) {
		nativeDomainRollback(ctx, tx)
		return nil, nativeDomainError("native_storage_unavailable")
	}
	return tx, nil
}
func nativeDomainRollback(ctx context.Context, tx pgx.Tx) {
	if tx == nil {
		return
	}
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(rollbackCtx)
}
func nativeDomainSourceTx(ctx context.Context, tx pgx.Tx, key uuid.UUID) (storagesource.SourceConfig, error) {
	var source storagesource.SourceConfig
	err := tx.QueryRow(ctx, `SELECT key,owner_id,installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled
 FROM bloem_storage_sources WHERE key=$1`, key).Scan(&source.Key, &source.OwnerID, &source.InstallationID, &source.PluginID, &source.ProviderSourceID, &source.RootEntryID, &source.ConfigurationRevision, &source.Enabled)
	return source, err
}
func nativeSourceRevision(actual, expected int64) error {
	if actual != expected {
		return &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentSourceRevision: &actual}
	}
	return nil
}
func sourceView(source storagesource.SourceConfig, owner resourcetenancy.Owner, state string, configured bool) SourceView {
	return SourceView{SourceKey: source.Key, OwnerKind: owner.Kind, OrganizationID: owner.OrganizationID, InstallationID: source.InstallationID,
		PluginID: source.PluginID, ProviderSourceID: source.ProviderSourceID, RootEntryID: source.RootEntryID, ConfigurationRevision: source.ConfigurationRevision,
		Enabled: source.Enabled, State: state, Configured: configured}
}
func (s *SourceManagement) GetSource(ctx context.Context, actor auth.AdminContextClaims, key uuid.UUID) (SourceView, error) {
	if key == uuid.Nil {
		return SourceView{}, nativeDomainError("invalid_request")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return SourceView{}, err
	}
	defer nativeDomainRollback(ctx, tx)
	view, err := s.sourceViewTx(ctx, tx, actor, key)
	if err != nil {
		return SourceView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	return view, nil
}
func (s *SourceManagement) sourceViewTx(ctx context.Context, tx pgx.Tx, actor auth.AdminContextClaims, key uuid.UUID) (SourceView, error) {
	source, err := nativeDomainSourceTx(ctx, tx, key)
	if err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	if err = resourcetenancy.NewStore(s.pool).RequireNativeManagementTx(ctx, tx, actor, key, nil, source.OwnerID, nil, false); err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	source, err = nativeDomainSourceTx(ctx, tx, key)
	if err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	var owner resourcetenancy.Owner
	err = tx.QueryRow(ctx, "SELECT id,kind,organization_id,revision FROM resource_owners WHERE id=$1", source.OwnerID).Scan(&owner.ID, &owner.Kind, &owner.OrganizationID, &owner.Revision)
	if err != nil {
		return SourceView{}, nativeDomainMap(err)
	}
	state := "detached"
	configured := false
	if source.InstallationID != nil {
		var enabled bool
		err = tx.QueryRow(ctx, "SELECT enabled,EXISTS(SELECT 1 FROM plugin_runtime_configs WHERE plugin_installation_id=$1) FROM plugin_installations WHERE id=$1", *source.InstallationID).Scan(&enabled, &configured)
		if err != nil {
			return SourceView{}, nativeDomainMap(err)
		}
		state = "attached"
		if !source.Enabled || !enabled {
			state = "disabled"
		}
	}
	return sourceView(source, owner, state, configured), nil
}

// This predicate is applied in SQL before the keyset LIMIT. Resource locks and
// retained actor checks still run for every projected row before it is returned.
const nativeVisibleSourceSQL = `($1::boolean OR (o.kind='organization' AND o.organization_id=$2) OR
 (o.kind='platform' AND s.installation_id IS NOT NULL AND EXISTS(SELECT 1 FROM organization_entitlements e
 WHERE e.organization_id=$2 AND e.root_owner_id=o.id AND e.root_kind='plugin_installation'
 AND e.entitlement_kind='plugin_availability' AND e.plugin_installation_id=s.installation_id AND e.status='active')))`
const nativeVisibleFolderSQL = `($1::boolean OR (fo.kind='organization' AND fo.organization_id=$2) OR
 (fo.kind='platform' AND EXISTS(SELECT 1 FROM organization_entitlements e WHERE e.organization_id=$2
 AND e.root_owner_id=fo.id AND e.root_kind='media_folder' AND e.entitlement_kind='library_access'
 AND e.media_folder_id=f.id AND e.status='active')))`

func (s *SourceManagement) ListSources(ctx context.Context, actor auth.AdminContextClaims, after *uuid.UUID, limit int) (SourcePage, error) {
	result := SourcePage{Sources: []SourceView{}}
	limit, err := nativePageLimit(limit)
	if err != nil {
		return result, err
	}
	if after != nil && *after == uuid.Nil {
		return result, nativeDomainError("invalid_request")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return result, err
	}
	defer nativeDomainRollback(ctx, tx)
	if _, err = resourcetenancy.NewStore(s.pool).RequireNativeLibraryCreateTx(ctx, tx, actor, nil); err != nil {
		return result, nativeDomainMap(err)
	}
	rows, err := tx.Query(ctx, `SELECT s.key FROM bloem_storage_sources s JOIN resource_owners o ON o.id=s.owner_id
 WHERE `+nativeVisibleSourceSQL+` AND ($3::uuid IS NULL OR s.key>$3) ORDER BY s.key LIMIT $4`, actor.Scope == auth.AdminScopePlatform, actor.OrganizationID, after, limit+1)
	if err != nil {
		return result, nativeDomainMap(err)
	}
	keys := []uuid.UUID{}
	for rows.Next() {
		var key uuid.UUID
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return result, nativeDomainMap(err)
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, nativeDomainMap(err)
	}
	// Finish the candidate read before resource authorization, which may involve
	// organizations other than the actor's owner and their independent lock order.
	if err = tx.Commit(ctx); err != nil {
		return result, nativeDomainMap(err)
	}
	for i, key := range keys {
		if i == limit {
			previous := keys[i-1]
			result.NextAfter = &previous
			break
		}
		view, e := s.GetSource(ctx, actor, key)
		if e != nil {
			return SourcePage{}, e
		}
		result.Sources = append(result.Sources, view)
	}
	return result, nil
}
func (s *SourceManagement) ListBindings(ctx context.Context, actor auth.AdminContextClaims, key uuid.UUID, after *uuid.UUID, limit int) (BindingPage, error) {
	result := BindingPage{Bindings: []storagesource.Binding{}}
	limit, err := nativePageLimit(limit)
	if err != nil {
		return result, err
	}
	if key == uuid.Nil || (after != nil && *after == uuid.Nil) {
		return result, nativeDomainError("invalid_request")
	}
	// Source inspection is required even for an empty binding result.
	if _, err = s.GetSource(ctx, actor, key); err != nil {
		return result, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return result, err
	}
	defer nativeDomainRollback(ctx, tx)
	if _, err = resourcetenancy.NewStore(s.pool).RequireNativeLibraryCreateTx(ctx, tx, actor, nil); err != nil {
		return result, nativeDomainMap(err)
	}
	rows, err := tx.Query(ctx, `SELECT b.id,b.source_key,b.folder_id FROM bloem_storage_bindings b
 JOIN media_folders f ON f.id=b.folder_id JOIN resource_owners fo ON fo.id=f.owner_id
 WHERE `+nativeVisibleFolderSQL+` AND b.source_key=$3 AND ($4::uuid IS NULL OR b.id>$4) ORDER BY b.id LIMIT $5`, actor.Scope == auth.AdminScopePlatform, actor.OrganizationID, key, after, limit+1)
	if err != nil {
		return result, nativeDomainMap(err)
	}
	bindings := []storagesource.Binding{}
	for rows.Next() {
		var b storagesource.Binding
		if err = rows.Scan(&b.ID, &b.SourceKey, &b.FolderID); err != nil {
			rows.Close()
			return result, nativeDomainMap(err)
		}
		bindings = append(bindings, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, nativeDomainMap(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return result, nativeDomainMap(err)
	}
	for i, b := range bindings {
		if i == limit {
			previous := bindings[i-1].ID
			result.NextAfter = &previous
			break
		}
		readTx, e := s.begin(ctx)
		if e != nil {
			return BindingPage{}, e
		}
		source, e := nativeDomainSourceTx(ctx, readTx, key)
		if e == nil {
			id := int64(b.FolderID)
			e = resourcetenancy.NewStore(s.pool).RequireNativeManagementTx(ctx, readTx, actor, key, nil, source.OwnerID, &id, false)
		}
		if e == nil {
			var actual storagesource.Binding
			e = readTx.QueryRow(ctx, "SELECT id,source_key,folder_id FROM bloem_storage_bindings WHERE id=$1", b.ID).Scan(&actual.ID, &actual.SourceKey, &actual.FolderID)
			if e == nil && actual != b {
				e = errors.New("binding changed")
			}
		}
		if e == nil {
			e = readTx.Commit(ctx)
		}
		nativeDomainRollback(ctx, readTx)
		if e != nil {
			return BindingPage{}, nativeDomainMap(e)
		}
		result.Bindings = append(result.Bindings, b)
	}
	return result, nil
}
