package handlers

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// A session or restart recipe identifies a source; it does not preserve a
// library grant. Use the freshly resolved viewer scope on every byte request.
//
// v1 handlers hold the request and read it through requestAccessFilter; v2
// handlers never read headers and carry only a context, so both spellings
// share one check.
func playbackLibraryAllowsFile(r *http.Request, file *models.MediaFile) bool {
	return fileAllowedByViewerScope(requestAccessFilter(r), file)
}

func playbackLibraryAllowsFileCtx(ctx context.Context, file *models.MediaFile) bool {
	return fileAllowedByViewerScope(AccessFilterFromContext(ctx, ""), file)
}

func fileAllowedByViewerScope(filter catalog.AccessFilter, file *models.MediaFile) bool {
	// A transcode may serve a lower resolution than its source. Admission owns
	// quality decisions; this delivery guard only rechecks library membership.
	return catalog.FileAllowedByAccess(file, catalog.AccessFilter{
		AllowedLibraryIDs:  filter.AllowedLibraryIDs,
		DisabledLibraryIDs: filter.DisabledLibraryIDs,
	})
}

func playbackLibraryAllowsSource(r *http.Request, resolver FilePathResolver, fileID int) bool {
	return sourceAllowedByViewerScope(r.Context(), requestAccessFilter(r), resolver, fileID)
}

func playbackLibraryAllowsSourceCtx(ctx context.Context, resolver FilePathResolver, fileID int) bool {
	return sourceAllowedByViewerScope(ctx, AccessFilterFromContext(ctx, ""), resolver, fileID)
}

func sourceAllowedByViewerScope(ctx context.Context, filter catalog.AccessFilter, resolver FilePathResolver, fileID int) bool {
	if filter.AllowedLibraryIDs == nil && len(filter.DisabledLibraryIDs) == 0 {
		return true
	}
	if resolver == nil {
		return false
	}
	file, err := resolver.GetByID(ctx, fileID)
	return err == nil && fileAllowedByViewerScope(filter, file)
}
