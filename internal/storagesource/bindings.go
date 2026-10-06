package storagesource

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FolderBinding looks up explicit retained binding state for a host-authorized
// folder. Disabled sources remain bound and cannot become filesystem fallback.
// The initial ingestion policy supports one source per library; a mixed binding
// is rejected explicitly rather than selecting an arbitrary source.
func (r *Repository) FolderBinding(ctx context.Context, authorizedFolderID int) (Binding, bool, error) {
	if authorizedFolderID <= 0 {
		return Binding{}, false, ErrReferenceConflict
	}
	rows, err := r.pool.Query(ctx, "SELECT id,source_key,folder_id FROM bloem_storage_bindings WHERE folder_id=$1 ORDER BY id LIMIT 2", authorizedFolderID)
	if err != nil {
		return Binding{}, false, err
	}
	defer rows.Close()
	var binding Binding
	count := 0
	for rows.Next() {
		count++
		if count > 1 {
			return Binding{}, false, ErrReferenceConflict
		}
		if err = rows.Scan(&binding.ID, &binding.SourceKey, &binding.FolderID); err != nil {
			return Binding{}, false, err
		}
	}
	if err = rows.Err(); err != nil {
		return Binding{}, false, err
	}
	return binding, count == 1, nil
}

// PendingIngestionRun lets a restarted worker resume a completed current
// discovery, including a run whose first ingestion checkpoint was not yet
// created. Superseded/finished generations are never returned. This method
// is a hint; BeginIngestion rechecks the full fence under locks.
func (r *Repository) PendingIngestionRun(ctx context.Context, bindingID uuid.UUID) (uuid.UUID, bool, error) {
	if bindingID == uuid.Nil {
		return uuid.Nil, false, ErrReferenceConflict
	}
	var run uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT r.id
 FROM bloem_storage_bindings b
 JOIN bloem_storage_sources s ON s.key=b.source_key
 JOIN bloem_storage_scan_runs r ON r.id=s.discovery_run_id AND r.source_key=s.key
 LEFT JOIN bloem_storage_ingestion x ON x.run_id=r.id AND x.binding_id=b.id
 WHERE b.id=$1 AND s.enabled AND r.state='complete'
 AND r.configuration_revision=s.configuration_revision AND NOT COALESCE(x.complete,false)`, bindingID).Scan(&run)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return run, err == nil, err
}
