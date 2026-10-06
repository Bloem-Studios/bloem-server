package storagesource

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// AttachFile associates an already host-authorized catalog file. A revision may
// change without changing the file identity; a binding or entry may not.
func (r *Repository) AttachFile(ctx context.Context, fileID int, ref PersistedRef) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = r.AttachFileTx(ctx, tx, fileID, ref); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AttachFileTx associates an already host-authorized file inside the caller's
// transaction. Catalog upsert and native-reference publication must commit
// together. The caller retains transaction ownership.
func (r *Repository) AttachFileTx(ctx context.Context, tx pgx.Tx, fileID int, ref PersistedRef) error {
	if tx == nil {
		return ErrReferenceConflict
	}
	location, err := CatalogLocation(ref.BindingID, ref.EntryID)
	if err != nil || fileID <= 0 || !validText(ref.Revision, 4096, true) || !validText(ref.LogicalPath, 65536, false) {
		return ErrReferenceConflict
	}
	var configRevision int64
	err = tx.QueryRow(ctx, `SELECT s.configuration_revision
 FROM media_files f
 JOIN bloem_storage_bindings b ON b.id=$2 AND b.folder_id=f.media_folder_id
 JOIN bloem_storage_sources s ON s.key=b.source_key
 JOIN bloem_storage_entries e ON e.source_key=s.key AND e.entry_id=$3
 WHERE f.id=$1 AND f.file_path=$4 AND e.revision=$5 AND e.logical_path=$6
 AND e.kind=1 AND e.configuration_revision=s.configuration_revision AND s.enabled
 FOR UPDATE OF s,b,e,f`, fileID, ref.BindingID, ref.EntryID, location, ref.Revision, ref.LogicalPath).Scan(&configRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrReferenceConflict
	}
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO bloem_storage_file_refs(media_file_id,binding_id,entry_id,revision,logical_path,configuration_revision)
 VALUES($1,$2,$3,$4,$5,$6)
 ON CONFLICT(media_file_id) DO UPDATE SET revision=EXCLUDED.revision,logical_path=EXCLUDED.logical_path,configuration_revision=EXCLUDED.configuration_revision
 WHERE bloem_storage_file_refs.binding_id=EXCLUDED.binding_id AND bloem_storage_file_refs.entry_id=EXCLUDED.entry_id`, fileID, ref.BindingID, ref.EntryID, ref.Revision, ref.LogicalPath, configRevision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrReferenceConflict
	}
	return nil
}

// FileReference receives a folder already authorized by host policy. Unavailable
// sources still return their typed reference; callers must not open the reserved
// catalog key as a local path.
func (r *Repository) FileReference(ctx context.Context, fileID, authorizedFolderID int) (SourceConfig, PersistedRef, error) {
	var source SourceConfig
	var ref PersistedRef
	var available bool
	var location string
	if fileID <= 0 || authorizedFolderID <= 0 {
		return source, ref, ErrReferenceConflict
	}
	err := r.pool.QueryRow(ctx, `SELECT s.key,s.owner_id,s.installation_id,s.plugin_id,s.provider_source_id,s.root_entry_id,s.configuration_revision,s.enabled,
 x.binding_id,x.entry_id,x.revision,x.logical_path,f.file_path,
 COALESCE(s.enabled AND i.enabled AND i.plugin_id=s.plugin_id AND i.owner_id=s.owner_id AND x.configuration_revision=s.configuration_revision,false)
 FROM media_files f
 JOIN bloem_storage_file_refs x ON x.media_file_id=f.id
 JOIN bloem_storage_bindings b ON b.id=x.binding_id AND b.folder_id=f.media_folder_id
 JOIN bloem_storage_sources s ON s.key=b.source_key
 LEFT JOIN plugin_installations i ON i.id=s.installation_id
 WHERE f.id=$1 AND f.media_folder_id=$2`, fileID, authorizedFolderID).Scan(&source.Key, &source.OwnerID, &source.InstallationID, &source.PluginID, &source.ProviderSourceID, &source.RootEntryID, &source.ConfigurationRevision, &source.Enabled, &ref.BindingID, &ref.EntryID, &ref.Revision, &ref.LogicalPath, &location, &available)
	if errors.Is(err, pgx.ErrNoRows) {
		return source, ref, ErrReferenceConflict
	}
	if err != nil {
		return source, ref, err
	}
	expected, err := CatalogLocation(ref.BindingID, ref.EntryID)
	if err != nil || location != expected {
		return SourceConfig{}, PersistedRef{}, ErrReferenceConflict
	}
	if !available {
		return source, ref, ErrSourceUnavailable
	}
	return source, ref, nil
}
