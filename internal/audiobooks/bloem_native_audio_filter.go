package audiobooks

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

const bloemCompatibleFileSQL = "NOT starts_with(file_path, 'bloem-storage:')"

func bloemCompatibleAudioFile(file *models.MediaFile) bool {
	return file != nil && !storagesource.IsNativeLocation(file.FilePath)
}

func bloemCompatibleAudioFiles(files []*models.MediaFile) []*models.MediaFile {
	out := make([]*models.MediaFile, 0, len(files))
	for _, file := range files {
		if bloemCompatibleAudioFile(file) {
			out = append(out, file)
		}
	}
	return out
}

func bloemCompatibleItemCondition(alias string, access catalog.AccessFilter, args *[]any, argIdx *int) string {
	condition := fmt.Sprintf("EXISTS (SELECT 1 FROM media_files compatible_audio WHERE compatible_audio.content_id=%s.content_id AND compatible_audio.missing_since IS NULL AND NOT starts_with(compatible_audio.file_path, 'bloem-storage:')", alias)
	appendScope := func(clause string, value any) {
		condition += fmt.Sprintf(clause, *argIdx)
		*args = append(*args, value)
		*argIdx = *argIdx + 1
	}
	if access.AllowedLibraryIDs != nil {
		appendScope(" AND compatible_audio.media_folder_id=ANY($%d::bigint[])", access.AllowedLibraryIDs)
	}
	if len(access.DisabledLibraryIDs) > 0 {
		appendScope(" AND NOT (compatible_audio.media_folder_id=ANY($%d::bigint[]))", access.DisabledLibraryIDs)
	}
	if access.ScopeFilesToLibrary && access.PresentationLibraryID != nil {
		appendScope(" AND compatible_audio.media_folder_id=$%d", *access.PresentationLibraryID)
	}
	return condition + ")"
}

func bloemCompatibleAudiobookIDs(ctx context.Context, pool *pgxpool.Pool, ids []string, access catalog.AccessFilter) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{ids}
	index := 2
	condition := bloemCompatibleItemCondition("mi", access, &args, &index)
	rows, err := pool.Query(ctx, `SELECT mi.content_id FROM media_items mi WHERE mi.content_id=ANY($1::text[]) AND `+condition, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *ABSMediaStore) bloemCompatibleAudiobookItems(ctx context.Context, items []*models.MediaItem, access catalog.AccessFilter) ([]*models.MediaItem, error) {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil {
			ids = append(ids, item.ContentID)
		}
	}
	allowed, err := bloemCompatibleAudiobookIDs(ctx, s.Pool, ids, access)
	if err != nil {
		return nil, err
	}
	out := make([]*models.MediaItem, 0, len(items))
	for _, item := range items {
		if item != nil && allowed[item.ContentID] {
			out = append(out, item)
		}
	}
	return out, nil
}
