package scanner

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
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

// maintenanceStaleItemCandidates returns the items of the whole-folder target
// list (maintenanceMembershipTargets with no prefix) that
// LibraryItemRepository.ReconcileItemsMembershipTx can act on: those whose
// membership here has no present file left in the folder, and those whose
// missing files here are all that is left of them, with no membership
// anywhere. Every other listed item it would leave alone.
//
// An item is on the target list when the maintenance owner has a file for it
// in the folder, or when it has a membership here and no file in the folder at
// all; for a file row of the last branch the first condition is the row's
// own. A file that is present ties its item to the folder, so that branch
// reads only missing files. The two membership branches stay separate
// statements of the UNION: joined by OR, Postgres can only answer them by
// hashing every media file twice (8 seconds for a 290,000-book library).
func (r *FileRepository) maintenanceStaleItemCandidates(ctx context.Context, folderID int) ([]string, error) {
	local := func(alias string) string { return r.maintenanceScope.Predicate(alias) }
	return collectStrings(ctx, r, `
		SELECT mil.content_id FROM media_item_libraries mil
		WHERE mil.media_folder_id = $1
		  AND NOT EXISTS (SELECT 1 FROM media_files x
			WHERE x.media_folder_id = mil.media_folder_id AND x.content_id = mil.content_id)
		UNION
		SELECT mil.content_id FROM media_item_libraries mil
		WHERE mil.media_folder_id = $1
		  AND NOT EXISTS (SELECT 1 FROM media_files p
			WHERE p.media_folder_id = mil.media_folder_id AND p.content_id = mil.content_id AND p.missing_since IS NULL)
		  AND EXISTS (SELECT 1 FROM media_files x
			WHERE x.media_folder_id = mil.media_folder_id AND x.content_id = mil.content_id AND (`+local("x")+`))
		UNION
		SELECT mf.content_id FROM media_files mf
		WHERE mf.media_folder_id = $1 AND mf.missing_since IS NOT NULL
		  AND mf.content_id IS NOT NULL AND mf.content_id <> '' AND (`+local("mf")+`)
		  AND NOT EXISTS (SELECT 1 FROM media_item_libraries m WHERE m.content_id = mf.content_id)
		  AND NOT EXISTS (SELECT 1 FROM media_files p
			WHERE p.media_folder_id = mf.media_folder_id AND p.content_id = mf.content_id AND p.missing_since IS NULL)`, folderID)
}

// maintenanceStaleEpisodeCandidates returns the episodes of the whole-folder
// target list whose span reaches a stale membership here, which is what
// EpisodeLibraryRepository.RemoveStaleEpisodeMemberships removes: memberships
// with no serving file in the folder (catalog.NoServingEpisodeFileSQL) among
// the listed episodes and the later ones of their season within
// catalog.MaxEpisodeSpan (catalog.SpanReachSQL). Widening this subset reaches
// no episode the whole list does not, and every stale one it does.
//
// An episode is on the target list when the maintenance owner has a file for
// it in the folder, or when it has a membership here and no file in the
// folder at all.
func (r *FileRepository) maintenanceStaleEpisodeCandidates(ctx context.Context, folderID int) ([]string, error) {
	return collectStrings(ctx, r, fmt.Sprintf(`
		WITH stale AS (
			SELECT el.episode_id FROM episode_libraries el
			WHERE el.media_folder_id = $1 AND %[1]s
		), reaching AS (
			SELECT s.episode_id AS listed FROM stale s
			UNION
			SELECT listed.content_id FROM stale s
			JOIN episodes later ON later.content_id = s.episode_id
			JOIN episodes listed ON listed.series_id = later.series_id
			     AND listed.season_number = later.season_number
			     AND later.episode_number BETWEEN listed.episode_number AND listed.episode_number + %[2]d
		)
		SELECT r.listed FROM reaching r
		WHERE EXISTS (SELECT 1 FROM media_files x WHERE x.media_folder_id = $1 AND x.episode_id = r.listed AND (%[3]s))
		   OR (EXISTS (SELECT 1 FROM episode_libraries el WHERE el.media_folder_id = $1 AND el.episode_id = r.listed)
		       AND NOT EXISTS (SELECT 1 FROM media_files x WHERE x.media_folder_id = $1 AND x.episode_id = r.listed))`,
		catalog.NoServingEpisodeFileSQL, catalog.MaxEpisodeSpan, r.maintenanceScope.Predicate("x")), folderID)
}

func collectStrings(ctx context.Context, r *FileRepository, query string, args ...any) ([]string, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// reconcileMaintenanceMemberships is the whole-folder reconcile under a
// maintenance scope. The items and episodes it may consider are those
// maintenanceMembershipTargets lists for the whole folder: the ones the
// maintenance owner's files name, and memberships with no file in the folder
// at all. Of those it passes on only the ones the removal can change, chosen
// in the database, so a large library's IDs never travel; the removals and
// the orphan purge decide exactly as they did over the whole lists.
func (s *Scanner) reconcileMaintenanceMemberships(ctx context.Context, folderID int, restoreEpisodes bool, protected []string) (int, int, []string, error) {
	if s.episodeLibraryRepo != nil {
		if restoreEpisodes {
			if err := s.episodeLibraryRepo.RestoreMaintenanceEpisodes(ctx, folderID, s.fileRepo.maintenanceScope.Predicate("mf")); err != nil {
				return 0, 0, nil, err
			}
		}
		episodes, err := s.fileRepo.maintenanceStaleEpisodeCandidates(ctx, folderID)
		if err != nil {
			return 0, 0, nil, err
		}
		if _, err = s.episodeLibraryRepo.RemoveStaleEpisodeMemberships(ctx, folderID, episodes); err != nil {
			return 0, 0, nil, err
		}
	}
	items, err := s.fileRepo.maintenanceStaleItemCandidates(ctx, folderID)
	if err != nil {
		return 0, 0, nil, err
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
