package scanner

import (
	"context"
	"strings"

	"github.com/Silo-Server/silo-server/internal/pathscope"
)

func (s *Scanner) SetMaintenanceFileScope(scope MaintenanceFileScope) {
	if s != nil && s.fileRepo != nil {
		s.fileRepo.maintenanceScope = scope
	}
}
func (r *FileRepository) maintenancePredicate(alias string) string {
	if r.maintenanceScope == nil {
		return ""
	}
	return " AND (" + r.maintenanceScope.Predicate(alias) + ")"
}

func (r *FileRepository) maintenanceScanStateByPrefix(ctx context.Context, folderID int, prefix string) ([]*scanStateFile, error) {
	if r.maintenanceScope == nil {
		return r.GetScanStateByFolderAndPathPrefix(ctx, folderID, prefix)
	}
	query, args := folderPathPrefixQuery(scanStateColumns, folderID, prefix)
	rows, err := r.pool.Query(ctx, strings.Replace(query, "\n\t\tORDER BY", r.maintenancePredicate("media_files")+"\n\t\tORDER BY", 1), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScanStateRows(rows)
}

func (r *FileRepository) maintenanceMembershipTargets(ctx context.Context, folderID int, prefix string, trash bool) ([]string, []string, error) {
	if r.maintenanceScope == nil {
		return r.ListMembershipTargets(ctx, folderID, prefix, trash)
	}
	args := []any{folderID}
	scope := ""
	if prefix != "" {
		args = append(args, prefix, pathscope.PrefixLike(prefix))
		scope = ` AND (mf.file_path=$2 OR mf.file_path LIKE $3 ESCAPE '\'`
		if trash {
			scope += ` OR mf.missing_since IS NOT NULL`
		}
		scope += `)`
	}
	query := `SELECT DISTINCT COALESCE(mf.content_id,''),COALESCE(mf.episode_id,'') FROM media_files mf WHERE mf.media_folder_id=$1` + scope + r.maintenancePredicate("mf")
	if prefix == "" {
		// Whole-folder cleanup must still find memberships after the last file
		// disappears. A prefix has no evidence tying such a membership to it.
		query += ` UNION SELECT mil.content_id,'' FROM media_item_libraries mil
            WHERE mil.media_folder_id=$1 AND NOT EXISTS (
                SELECT 1 FROM media_files mf WHERE mf.media_folder_id=mil.media_folder_id AND mf.content_id=mil.content_id)
            UNION SELECT '',el.episode_id FROM episode_libraries el
            WHERE el.media_folder_id=$1 AND NOT EXISTS (
                SELECT 1 FROM media_files mf WHERE mf.media_folder_id=el.media_folder_id AND mf.episode_id=el.episode_id)`
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var items, episodes []string
	for rows.Next() {
		var item, episode string
		if err := rows.Scan(&item, &episode); err != nil {
			return nil, nil, err
		}
		if item != "" {
			items = append(items, item)
		}
		if episode != "" {
			episodes = append(episodes, episode)
		}
	}
	return items, episodes, rows.Err()
}
func (s *Scanner) reconcileMaintenanceMemberships(ctx context.Context, folderID int, restoreEpisodes bool, protected []string) (int, int, []string, error) {
	items, episodes, err := s.fileRepo.maintenanceMembershipTargets(ctx, folderID, "", true)
	if err != nil {
		return 0, 0, nil, err
	}
	if s.episodeLibraryRepo != nil {
		if restoreEpisodes {
			if err = s.episodeLibraryRepo.RestoreMaintenanceEpisodes(ctx, folderID, s.fileRepo.maintenanceScope.Predicate("mf")); err != nil {
				return 0, 0, nil, err
			}
		}
		if _, err = s.episodeLibraryRepo.RemoveStaleEpisodeMemberships(ctx, folderID, episodes); err != nil {
			return 0, 0, nil, err
		}
	}
	retained, err := s.fileRepo.maintenanceRetainedItems(ctx, items)
	if err != nil {
		return 0, 0, nil, err
	}
	// Use the transactional global-presence guard even for a local candidate
	// shared with another library or a native version.
	return s.reconcileMaintenanceItems(ctx, folderID, items, protected, retained)
}

func (s *Scanner) reconcileMaintenanceItems(ctx context.Context, folderID int, items, protected, retained []string) (int, int, []string, error) {
	tx, err := s.fileRepo.pool.Begin(ctx)
	if err != nil {
		return 0, 0, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	removed, deleted, dirs, err := s.libraryRepo.ReconcileItemsMembershipTx(ctx, tx, folderID, items, protected, retained)
	if err != nil {
		return 0, 0, nil, err
	}
	err = tx.Commit(ctx)
	return removed, deleted, dirs, err
}

// Excluded versions anywhere in the catalog protect a candidate item against
// cascading deletion, including rows already marked missing by their owner.
func (r *FileRepository) maintenanceRetainedItems(ctx context.Context, candidates []string) ([]string, error) {
	if r.maintenanceScope == nil || len(candidates) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT mf.content_id FROM media_files mf WHERE mf.content_id=ANY($1::text[]) AND NOT (`+r.maintenanceScope.Predicate("mf")+`)`, candidates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Another owner's version keeps the item discoverable for its later orphan
// check, so this owner can release its own expired row without a retention cycle.
func (r *FileRepository) maintenanceTrashGuard(guard, alias string) string {
	if r.maintenanceScope == nil {
		return guard
	}
	return "(" + guard + " OR EXISTS (SELECT 1 FROM media_files retained WHERE retained.content_id=" + alias + ".content_id AND NOT (" + r.maintenanceScope.Predicate("retained") + ")))"
}
