package downloads

import (
	"context"
	"sync/atomic"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// GuardNativeStorageFiles hides native identities (storage-source files and
// `.strm` items) from download resolution, including retained rows when the
// native runtime is disabled. Install at composition regardless of whether a
// native runtime is available. Once SetNativeOriginals installs a reader, the
// guard admits the native videos that reader can serve as originals; every
// other native row stays hidden.
func GuardNativeStorageFiles(files FileResolver) FileResolver {
	if files == nil {
		return nil
	}
	return &nativeStorageFiles{FileResolver: files}
}

type nativeStorageFiles struct {
	FileResolver
	native atomic.Pointer[nativeOriginalsBox]
}

type nativeOriginalsBox struct{ NativeOriginals }

func (r *nativeStorageFiles) originals() NativeOriginals {
	if box := r.native.Load(); box != nil {
		return box.NativeOriginals
	}
	return nil
}

// admit returns the download view of a native file, or nil when it is hidden.
func (r *nativeStorageFiles) admit(ctx context.Context, file *models.MediaFile) *models.MediaFile {
	native := r.originals()
	if native == nil {
		return nil
	}
	view, ok := native.DownloadFile(ctx, file)
	if !ok || view == nil || view.ID != file.ID || view.FilePath != file.FilePath {
		return nil
	}
	return view
}

func (r *nativeStorageFiles) GetByID(ctx context.Context, id int) (*models.MediaFile, error) {
	file, err := r.FileResolver.GetByID(ctx, id)
	if err == nil && file != nil && storagesource.IsNativeLocation(file.FilePath) {
		if view := r.admit(ctx, file); view != nil {
			return view, nil
		}
		return nil, catalog.ErrItemNotFound
	}
	return file, err
}

func (r *nativeStorageFiles) candidates(ctx context.Context, files []*models.MediaFile) []*models.MediaFile {
	if files == nil {
		return nil
	}
	out := make([]*models.MediaFile, 0, len(files))
	for _, file := range files {
		if file == nil || !storagesource.IsNativeLocation(file.FilePath) {
			out = append(out, file)
		} else if view := r.admit(ctx, file); view != nil {
			out = append(out, view)
		}
	}
	return out
}

func (r *nativeStorageFiles) GetByContentID(ctx context.Context, id string) ([]*models.MediaFile, error) {
	files, err := r.FileResolver.GetByContentID(ctx, id)
	if err != nil {
		return files, err
	}
	return r.candidates(ctx, files), nil
}

func (r *nativeStorageFiles) GetByEpisodeID(ctx context.Context, id string) ([]*models.MediaFile, error) {
	files, err := r.FileResolver.GetByEpisodeID(ctx, id)
	if err != nil {
		return files, err
	}
	return r.candidates(ctx, files), nil
}

func (r *nativeStorageFiles) ListByEpisodeIDs(ctx context.Context, ids []string) (map[string][]*models.MediaFile, error) {
	files, err := r.FileResolver.ListByEpisodeIDs(ctx, ids)
	if err != nil || files == nil {
		return files, err
	}
	out := make(map[string][]*models.MediaFile, len(files))
	for id, candidates := range files {
		out[id] = r.candidates(ctx, candidates)
	}
	return out, nil
}
