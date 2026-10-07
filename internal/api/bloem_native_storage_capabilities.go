package api

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

// This private witness is installed only at the router's final finite-wrapper
// composition. It holds actual services; callers cannot supply ready booleans.
type nativeStorageCapabilities struct {
	deps  Dependencies
	guard *nativeMutationGuard
	v2    *apiv2.Dependencies
}

func (c *nativeStorageCapabilities) attach(g *nativeMutationGuard, v2 *apiv2.Dependencies) {
	if c == nil || c.deps.NativeStorageManagement == nil {
		return
	}
	c.guard, c.v2 = g, v2
}
func (c *nativeStorageCapabilities) composed() bool {
	if c == nil || c.guard == nil || c.v2 == nil {
		return false
	}
	d, g := c.deps, c.guard
	h := d.NativeStorageManagement
	if d.DB == nil || h == nil || d.NativeStorage == nil || h.Registry != d.NativeStorage.Registry || d.FolderRepo == nil || d.FileRepo == nil || d.FileRepo.Pool() != d.DB || d.LibraryScanQueue == nil || d.LibraryIngester == nil || d.Scanner == nil || g.pool != d.DB || g.folders != d.FolderRepo || g.library == nil || !g.curation || !g.access.NativeStorageComposition() {
		return false
	}
	// Every exposed mutation service must still be the installed finite wrapper.
	// Optional absent host services do not create an unguarded operation.
	for _, service := range []any{c.v2.LibraryAdmin, c.v2.ScanControls, c.v2.AdminItemMetadata, c.v2.AdminCatalogMatch, c.v2.AdminCatalogSplit, c.v2.AdminCatalogImages, c.v2.AdminMetadataTranslation, c.v2.MetadataAI, c.v2.CatalogTrailers} {
		if service == nil {
			continue
		}
		wrapped, ok := service.(*nativeMutationServices)
		if !ok || wrapped == nil || wrapped.guard != g || wrapped.admit == nil {
			return false
		}
	}
	if c.v2.LibraryAdmin == nil || c.v2.ScanControls == nil {
		return false
	}
	files, ok := c.v2.EbookFiles.(*handlers.NativeEbookFileService)
	if !ok || files == nil || files.Local == nil || files.Local.FileAuthorizer == nil || files.Local.FileAuthorizer.FileResolver != d.FileRepo || files.Local.FileAuthorizer.ItemAccess == nil {
		return false
	}
	coordinator, ok := files.Native.(*nativestorage.Coordinator)
	return ok && h.Sources.NativeStorageComposition(d.DB, d.NativeStorage, coordinator) && h.Libraries.NativeStorageComposition(d.DB, d.FolderRepo, d.LibraryScanQueue) && d.LibraryScanQueue.NativeStorageComposition(d.DB, d.FolderRepo, d.LibraryIngester) && d.LibraryIngester.NativeStorageComposition(d.DB, d.NativeStorage, d.FolderRepo, d.Scanner)
}
func (c *nativeStorageCapabilities) NativeStorageReady(ctx context.Context) bool {
	return ctx != nil && ctx.Err() == nil && c.composed() && catalog.NativeStorageSchemaReady(ctx, c.deps.DB) && c.composed()
}
