package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// Native video candidates enter the existing access and preflight checks only
// when the host installs its authorized source reader. Return a fresh list so
// multipart selection never mutates the catalog repository's slice.
func (h *PlaybackHandler) playbackVersionFiles() PlaybackFileVersionFetcher {
	if h == nil {
		return nil
	}
	if h.NativeVideo == nil {
		return guardNativePlaybackVersions(h.FileVersionFetcher)
	}
	if h.FileVersionFetcher == nil {
		return nil
	}
	return nativeVideoVersions{h.FileVersionFetcher}
}

type nativeVideoVersions struct{ PlaybackFileVersionFetcher }

func (v nativeVideoVersions) GetByContentID(ctx context.Context, id string) ([]*models.MediaFile, error) {
	files, err := v.PlaybackFileVersionFetcher.GetByContentID(ctx, id)
	if err != nil {
		return nil, err
	}
	return nativeVideoVersionCandidates(files), nil
}

func (v nativeVideoVersions) GetByEpisodeID(ctx context.Context, id string) ([]*models.MediaFile, error) {
	files, err := v.PlaybackFileVersionFetcher.GetByEpisodeID(ctx, id)
	if err != nil {
		return nil, err
	}
	return nativeVideoVersionCandidates(files), nil
}

func nativeVideoVersionCandidates(files []*models.MediaFile) []*models.MediaFile {
	if files == nil {
		return nil
	}
	result := make([]*models.MediaFile, 0, len(files))
	for _, file := range files {
		if file != nil && (!storagesource.IsNativeLocation(file.FilePath) || nativeVideoFileSupported(file)) {
			result = append(result, file)
		}
	}
	return result
}
