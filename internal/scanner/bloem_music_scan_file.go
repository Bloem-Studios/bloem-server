package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

// walkModeMusic walks a music library: audio extensions, no skipping.
const walkModeMusic = walkModeEbook + 1

// scanMusicFolderResult is ScanFolder's music-library branch.
func (s *Scanner) scanMusicFolderResult(ctx context.Context, folder *models.MediaFolder) (*ScanResult, error) {
	if err := s.ScanMusicFolder(ctx, folder, true); err != nil {
		return nil, err
	}
	return &ScanResult{}, nil
}

// scanMusicSubtreeResult is ScanSubtree's music-library branch.
func (s *Scanner) scanMusicSubtreeResult(ctx context.Context, folder *models.MediaFolder, cleanSubtree string) (*ScanResult, error) {
	if err := s.scanMusicSubtree(ctx, folder, []string{cleanSubtree}); err != nil {
		return nil, err
	}
	return &ScanResult{}, nil
}

// scanMusicFile is ScanFile's music-library branch: a vanished file is
// reconciled in place, anything else rescans the file's album directory.
func (s *Scanner) scanMusicFile(ctx context.Context, folder *models.MediaFolder, cleanFile string) error {
	if !SupportsAudioFile(cleanFile) {
		return fmt.Errorf("unrecognized audio extension: %s", strings.ToLower(filepath.Ext(cleanFile)))
	}
	if _, err := os.Stat(cleanFile); errors.Is(err, os.ErrNotExist) {
		if s.fileRepo == nil {
			return nil
		}
		handled, _, reconcileErr := s.reconcileVanishedMusicFile(ctx, folder, cleanFile)
		if reconcileErr != nil {
			return reconcileErr
		}
		if !handled {
			return s.scanMusicSubtree(ctx, folder, []string{filepath.Dir(cleanFile)})
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("stat music file %s: %w", cleanFile, err)
	}
	return s.scanMusicSubtree(ctx, folder, []string{filepath.Dir(cleanFile)})
}

// reconcileVanishedMusicFile atomically marks one folder-owned music file
// missing, removes its track, and reconciles only that item's membership. The
// folder mutation lock serializes the operation with music ingest across
// server processes. The second stat after acquiring the lock prevents a stale
// absence check from winning after another scanner restored the path. The two
// booleans report whether the missing event was handled and whether catalog
// state changed; an unowned path is a handled no-op. A path beneath an
// unreachable or suspect-empty configured root is also a handled no-op: a
// dropped mount makes every file under it look vanished, and that outage must
// not mark files missing or delete their tracks.
func (s *Scanner) reconcileVanishedMusicFile(ctx context.Context, folder *models.MediaFolder, filePath string) (bool, bool, error) {
	folderID := folder.ID
	// Probe before taking the folder lock so a hung mount cannot stall music
	// ingest for the probe timeout. ScanFile may receive a scoped folder clone,
	// so observe every configured root.
	configuredPaths, err := s.configuredFolderPaths(ctx, folder)
	if err != nil {
		return false, false, err
	}
	observation, err := s.ObserveRoots(ctx, folderID, configuredPaths)
	if err != nil {
		return false, false, err
	}
	protectedRoots := append(append([]string(nil), observation.UnreachableRoots...), observation.SuspectEmptyRoots...)
	if pathWithinAnyRoot(filePath, protectedRoots) {
		return true, false, nil
	}

	tx, err := s.fileRepo.Pool().Begin(ctx)
	if err != nil {
		return false, false, fmt.Errorf("begin vanished music reconciliation: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := lockMusicFolderMutationExclusiveTx(ctx, tx, folderID); err != nil {
		return false, false, err
	}
	albumRoot := filepath.Dir(filePath)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, musicAlbumRootLockKey(folderID, albumRoot)); err != nil {
		return false, false, fmt.Errorf("lock vanished music album root: %w", err)
	}

	var fileID int
	var contentID string
	err = tx.QueryRow(ctx, `
		SELECT id, COALESCE(content_id, '')
		FROM media_files
		WHERE media_folder_id = $1
		  AND file_path = $2
		  AND base_type = 'music'
		FOR UPDATE
	`, folderID, filePath).Scan(&fileID, &contentID)
	rowMissing := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !rowMissing {
		return false, false, fmt.Errorf("lock vanished music file: %w", err)
	}
	// The caller's first stat happened before it waited for the folder lock.
	// Recheck even when no catalog row exists: a path created while waiting
	// must fall through to normal ingest instead of becoming a handled no-op.
	if _, err := os.Stat(filePath); err == nil {
		return false, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, false, fmt.Errorf("recheck music file %s: %w", filePath, err)
	}
	if rowMissing {
		return true, false, nil
	}

	tag, err := tx.Exec(ctx, `
		UPDATE media_files
		SET missing_since = $1, updated_at = NOW()
		WHERE id = $2
		  AND media_folder_id = $3
		  AND file_path = $4
		  AND base_type = 'music'
	`, time.Now().UTC(), fileID, folderID, filePath)
	if err != nil {
		return false, false, fmt.Errorf("mark vanished music file missing: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return true, false, nil
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM music_tracks mt USING media_files mf
		WHERE mt.media_file_id = $1
		  AND mf.id = mt.media_file_id
		  AND mf.media_folder_id = $2
		  AND mf.file_path = $3
		  AND mf.base_type = 'music'
		  AND mf.missing_since IS NOT NULL
	`, fileID, folderID, filePath); err != nil {
		return false, false, fmt.Errorf("delete vanished music track: %w", err)
	}
	if s.libraryRepo != nil {
		if _, _, _, err := s.libraryRepo.ReconcileContentMembershipTx(ctx, tx, folderID, contentID, protectedRoots); err != nil {
			return false, false, fmt.Errorf("reconcile vanished music item: %w", err)
		}
	}
	if err := s.syncMusicScopedLibraryStateTx(ctx, tx, folderID, []musicRepairScope{{
		contentID: contentID,
		albumRoot: albumRoot,
	}}); err != nil {
		return false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, fmt.Errorf("commit vanished music reconciliation: %w", err)
	}
	return true, true, nil
}
