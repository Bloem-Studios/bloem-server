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
	return nativePlaybackFiles{files}
}

type nativePlaybackFiles struct{ FilePathResolver }

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
