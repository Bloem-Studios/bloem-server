package scanner

import (
	"context"
	"errors"
	"strings"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

// Request scans use the stored path winner, never just the incoming tuple.
// The existing writer transaction (when supplied) owns the row wait; ordinary
// scans/native enrichment without a request origin incur no additional query.
func requireNativeStoredFiles(ctx context.Context, tx pgx.Tx, files []models.MediaFile) error {
	if !catalog.NativePhaseRequest(ctx) {
		return nil
	}
	selected := catalog.NativePhaseTargets{}
	for _, file := range files {
		var id int
		err := tx.QueryRow(ctx, `SELECT id FROM media_files WHERE file_path=$1 FOR UPDATE`, file.FilePath).Scan(&id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			selected.FileIDs = append(selected.FileIDs, id)
		}
		selected.LibraryIDs = append(selected.LibraryIDs, file.MediaFolderID)
		for _, key := range []string{file.ContentID, file.EpisodeID, file.ExtraID} {
			if key != "" {
				selected.ContentIDs = append(selected.ContentIDs, key)
			}
		}
	}
	return catalog.RequireNativePhase(ctx, tx, selected)
}

// upsertNativeFile reuses the canonical SQL/argument normalization. Only request
// pool calls create a transaction; a caller transaction remains caller-owned.
func (r *FileRepository) upsertNativeFile(ctx context.Context, tx pgx.Tx, file models.MediaFile) (*models.MediaFile, error) {
	own := tx == nil
	if own {
		var err error
		tx, err = r.pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx) //nolint:errcheck
	}
	capture := &fileUpsertCapture{}
	if _, err := r.upsertWithQueryer(ctx, capture, file); err != nil {
		return nil, err
	}
	// An absent key must never execute the updating conflict branch. A unique
	// conflict returns no row and restarts actual locked-winner selection instead.
	conflict := strings.Index(capture.query, "ON CONFLICT (file_path) DO UPDATE SET")
	returning := strings.LastIndex(capture.query, "RETURNING ")
	if conflict < 0 || returning < conflict {
		return nil, &catalog.NativePhaseRefusal{Code: nativeStorageUnavailableCode}
	}
	insert := capture.query[:conflict] + "ON CONFLICT (file_path) DO NOTHING\n\t" + capture.query[returning:]
	for attempt := 0; attempt < 3; attempt++ {
		var winner int
		err := tx.QueryRow(ctx, `SELECT id FROM media_files WHERE file_path=$1 FOR UPDATE`, file.FilePath).Scan(&winner)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		selected := nativeFileTargets(file)
		if err == nil {
			selected.FileIDs = []int{winner}
		}
		if err := catalog.RequireNativePhase(ctx, tx, selected); err != nil {
			return nil, err
		}
		query := capture.query
		if winner == 0 {
			query = insert
		}
		saved, err := scanMediaFile(tx.QueryRow(ctx, query, capture.args...))
		if errors.Is(err, ErrFileNotFound) && winner == 0 {
			continue
		}
		if err != nil {
			return nil, err
		}
		if own {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
		}
		return saved, nil
	}
	return nil, &catalog.NativePhaseRefusal{Code: nativeStorageUnavailableCode}
}

func nativeFileTargets(file models.MediaFile) catalog.NativePhaseTargets {
	selected := catalog.NativePhaseTargets{LibraryIDs: []int{file.MediaFolderID}}
	for _, id := range []string{file.ContentID, file.EpisodeID, file.ExtraID} {
		if id != "" {
			selected.ContentIDs = append(selected.ContentIDs, id)
		}
	}
	return selected
}

func (r *FileRepository) updateNativeFileIdentity(ctx context.Context, file models.MediaFile, subtitles []byte) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var winner int
	for attempt := 0; attempt < 3; attempt++ {
		err = tx.QueryRow(ctx, `SELECT id FROM media_files WHERE file_path=$1 FOR UPDATE`, file.FilePath).Scan(&winner)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
	}
	if err != nil {
		return 0, ErrFileNotFound
	}
	selected := nativeFileTargets(file)
	selected.FileIDs = []int{winner}
	if err := catalog.RequireNativePhase(ctx, tx, selected); err != nil {
		return 0, err
	}
	id, err := r.updateIdentityWithQueryer(ctx, tx, file, subtitles)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}
