package api

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func nativeAudioPresentation(deps Dependencies) func(context.Context, *models.MediaFile) error {
	if deps.DB == nil {
		return nil
	}
	refs := storagesource.NewRepository(deps.DB)
	return func(ctx context.Context, file *models.MediaFile) error {
		if !handlers.NativeAudioSupported(file) {
			return storagesource.ErrReferenceConflict
		}
		ids, err := refs.PresentationReferences(ctx, file.MediaFolderID, file.ContentID, file.PresentationGroupKey)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id == file.ID {
				return nil
			}
		}
		return storagesource.ErrReferenceConflict
	}
}

// attachNativeAudioReadiness releases the scanner latch only after delivery
// and whole-presentation authority have been composed on the same service.
func attachNativeAudioReadiness(deps Dependencies, h *handlers.PlaybackHandler, stream *handlers.StreamHandler, libraries *handlers.LibraryHandler) {
	audio := deps.NativeAudioScannerReady && deps.NativeAudioGate != nil && h != nil && h.NativeVideo != nil && h.NativeVideo.Presentation != nil && stream != nil && stream.NativeVideo == h.NativeVideo
	if h != nil && h.NativeVideo != nil {
		h.NativeVideo.AudioReady = audio
	}
	if libraries != nil {
		if locations, ok := libraries.StorageLocations.(interface{ SetNativeAudioReady(bool) }); ok {
			locations.SetNativeAudioReady(audio)
		}
	}
	if deps.NativeStorageManagement != nil {
		if c, ok := deps.NativeStorageManagement.Capabilities.(*nativeStorageCapabilities); ok && c != nil {
			c.audio = audio
		}
	}
	if deps.NativeAudioGate != nil {
		deps.NativeAudioGate.Store(audio)
	}
}
