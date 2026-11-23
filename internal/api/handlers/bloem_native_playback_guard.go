package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// Native storage files have opaque catalog identities, never OS filenames.
// Unsupported playback must refuse them before local probing or missing-file
// checks. The ebook reader resolves and opens native files independently.
func guardNativePlaybackFiles(files FilePathResolver) FilePathResolver {
	if files == nil {
		return nil
	}
	// The batched lookup the catalog's file repository offers, which Silo's
	// paused-timeout sweep looks for on the handler's resolver
	// (pausedCandidateFiles).
	if batch, ok := files.(interface {
		GetByIDs(ctx context.Context, ids []int) ([]*models.MediaFile, error)
	}); ok {
		return nativePlaybackFileBatch{nativePlaybackFiles{files}, batch.GetByIDs}
	}
	return nativePlaybackFiles{files}
}

type nativePlaybackFiles struct{ FilePathResolver }

// nativePlaybackFileBatch keeps the inner resolver's batched lookup behind the
// guard, leaving native storage files out as GetByID refuses them.
type nativePlaybackFileBatch struct {
	nativePlaybackFiles
	getByIDs func(ctx context.Context, ids []int) ([]*models.MediaFile, error)
}

func (r nativePlaybackFileBatch) GetByIDs(ctx context.Context, ids []int) ([]*models.MediaFile, error) {
	files, err := r.getByIDs(ctx, ids)
	if err != nil {
		return files, err
	}
	return filesystemPlaybackCandidates(files), nil
}

func (r nativePlaybackFiles) GetByID(ctx context.Context, id int) (*models.MediaFile, error) {
	file, err := r.FilePathResolver.GetByID(ctx, id)
	if err == nil && file != nil && storagesource.IsNativeLocation(file.FilePath) {
		return nil, scanner.ErrFileNotFound
	}
	return file, err
}

// Guard at consumption because callers can assign a version fetcher directly.
// Filtering both getters also protects alternate editions and multipart resume.
func guardNativePlaybackVersions(files PlaybackFileVersionFetcher) PlaybackFileVersionFetcher {
	if files == nil {
		return nil
	}
	return nativePlaybackVersions{files}
}

type nativePlaybackVersions struct{ PlaybackFileVersionFetcher }

func (r nativePlaybackVersions) GetByContentID(ctx context.Context, id string) ([]*models.MediaFile, error) {
	files, err := r.PlaybackFileVersionFetcher.GetByContentID(ctx, id)
	if err != nil {
		return files, err
	}
	return filesystemPlaybackCandidates(files), nil
}

func (r nativePlaybackVersions) GetByEpisodeID(ctx context.Context, id string) ([]*models.MediaFile, error) {
	files, err := r.PlaybackFileVersionFetcher.GetByEpisodeID(ctx, id)
	if err != nil {
		return files, err
	}
	return filesystemPlaybackCandidates(files), nil
}

func filesystemPlaybackCandidates(files []*models.MediaFile) []*models.MediaFile {
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
