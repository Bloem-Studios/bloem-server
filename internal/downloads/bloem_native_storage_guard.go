package downloads

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// GuardNativeStorageFiles hides native identities from unsupported download
// resolution, including retained rows when the native runtime is disabled.
// Install at composition regardless of whether a native runtime is available.
func GuardNativeStorageFiles(files FileResolver) FileResolver {
	if files == nil {
		return nil
	}
	return nativeStorageFiles{FileResolver: files}
}

type nativeStorageFiles struct{ FileResolver }

func (r nativeStorageFiles) GetByID(ctx context.Context, id int) (*models.MediaFile, error) {
	file, err := r.FileResolver.GetByID(ctx, id)
	if err == nil && file != nil && storagesource.IsNativeLocation(file.FilePath) {
		return nil, catalog.ErrItemNotFound
	}
	return file, err
}

func nativeDownloadCandidates(files []*models.MediaFile) []*models.MediaFile {
	if files == nil {
		return nil
	}
	out := make([]*models.MediaFile, 0, len(files))
	for _, file := range files {
		if file == nil || !storagesource.IsNativeLocation(file.FilePath) {
			out = append(out, file)
		}
	}
	return out
}

func (r nativeStorageFiles) GetByContentID(ctx context.Context, id string) ([]*models.MediaFile, error) {
	files, err := r.FileResolver.GetByContentID(ctx, id)
	if err != nil {
		return files, err
	}
	return nativeDownloadCandidates(files), nil
}

func (r nativeStorageFiles) GetByEpisodeID(ctx context.Context, id string) ([]*models.MediaFile, error) {
	files, err := r.FileResolver.GetByEpisodeID(ctx, id)
	if err != nil {
		return files, err
	}
	return nativeDownloadCandidates(files), nil
}

func (r nativeStorageFiles) ListByEpisodeIDs(ctx context.Context, ids []string) (map[string][]*models.MediaFile, error) {
	files, err := r.FileResolver.ListByEpisodeIDs(ctx, ids)
	if err != nil || files == nil {
		return files, err
	}
	out := make(map[string][]*models.MediaFile, len(files))
	for id, candidates := range files {
		out[id] = nativeDownloadCandidates(candidates)
	}
	return out, nil
}
