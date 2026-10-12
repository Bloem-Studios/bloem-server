package api

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// nativeStorageCapabilities reports whether this router composed the storage
// source services: the host runtime, the source management service and the
// reader that opens storage files.
type nativeStorageCapabilities struct {
	deps        Dependencies
	reader      bool
	locations   bool
	video       bool
	movies      bool
	remoteVideo bool
}

func (c *nativeStorageCapabilities) NativeStorageReady(ctx context.Context) bool {
	if c == nil || ctx == nil || ctx.Err() != nil || !c.reader {
		return false
	}
	d := c.deps
	h := d.NativeStorageManagement
	return d.DB != nil && d.NativeStorage != nil && h != nil && h.Sources != nil && h.Registry == d.NativeStorage.Registry && d.LibraryIngester != nil
}

func (c *nativeStorageCapabilities) InitialRootDiscoveryReady(ctx context.Context) bool {
	return c.NativeStorageReady(ctx) && c.deps.NativeStorageManagement.Sources.InitialRootDiscoveryReady()
}

func (c *nativeStorageCapabilities) NativeVideoReady(ctx context.Context) bool {
	return c.NativeStorageReady(ctx) && c.video
}

func (c *nativeStorageCapabilities) NativeMovieLibrariesReady(ctx context.Context) bool {
	return c.NativeVideoReady(ctx) && c.movies
}

func (c *nativeStorageCapabilities) NativeRemoteVideoReady(ctx context.Context) bool {
	return c.NativeVideoReady(ctx) && c.remoteVideo
}

// attachNativeVideoReadiness runs once before the router accepts requests.
// An ingestor flag alone cannot admit a library whose playback was not wired.
func attachNativeVideoReadiness(deps Dependencies, h *handlers.PlaybackHandler, stream *handlers.StreamHandler, libraries *handlers.LibraryHandler) {
	if h == nil || h.NativeVideo == nil || stream == nil || stream.NativeVideo != h.NativeVideo {
		return
	}
	movies := false
	if deps.NativeMovieScannerReady && libraries != nil {
		if locations, ok := libraries.StorageLocations.(interface{ SetNativeMovieReady(bool) }); ok {
			locations.SetNativeMovieReady(true)
			movies = true
		}
	}
	if deps.NativeStorageManagement == nil {
		return
	}
	if c, ok := deps.NativeStorageManagement.Capabilities.(*nativeStorageCapabilities); ok && c != nil {
		c.video = true
		c.movies = movies
		c.remoteVideo = h.NativeVideo.WorkerGrants.Enabled() && h.NodeRecipeStore != nil && h.NodeRecipeStore.Enabled()
	}
}

func (c *nativeStorageCapabilities) LibraryLocationBrowsingReady(ctx context.Context) bool {
	return c.NativeStorageReady(ctx) && c.deps.NativeStorageManagement.Browser != nil && c.deps.RedisClient != nil
}
func (c *nativeStorageCapabilities) LibraryLocationEditingReady(ctx context.Context) bool {
	return c.LibraryLocationBrowsingReady(ctx) && c.locations && c.deps.NativeStorageManagement.Locations != nil
}

// Mixing reads the domain's single save-guard switch, set by mixed ingestion.
func (c *nativeStorageCapabilities) LibraryLocationMixingReady(ctx context.Context) bool {
	return c.LibraryLocationEditingReady(ctx) && c.deps.NativeStorageManagement.Locations.Locations.MixedLocationsReady()
}

// Audio requires full Task9 scanner/catalog/playback composition.
func (c *nativeStorageCapabilities) NativeAudioReady(context.Context) bool { return false }
