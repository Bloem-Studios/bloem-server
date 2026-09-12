package handlers

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// A session or restart recipe identifies a source; it does not preserve a
// library entitlement. Use the freshly resolved viewer scope on every byte
// request. The scope is already bounded to the viewer's organization by the
// policy engine, so rechecking library membership rechecks the tenant.
//
// v1 handlers hold the request and read it through requestAccessFilter; v2
// handlers never read headers and carry only a context, so both spellings
// share one check. Every path below fails closed when no scope was ever
// resolved into context (access.GetScope's ok is false) rather than treating
// an absent scope as an unrestricted one — AccessFilterFromContext collapses
// that distinction into a zero-value AccessFilter, which is exactly why this
// package checks access.GetScope directly instead of trusting it alone.
func playbackLibraryAllowsFile(r *http.Request, file *models.MediaFile) bool {
	return fileAllowedByViewerScopeCtx(r.Context(), requestAccessFilter(r), file)
}

func playbackLibraryAllowsFileCtx(ctx context.Context, file *models.MediaFile) bool {
	return fileAllowedByViewerScopeCtx(ctx, AccessFilterFromContext(ctx, ""), file)
}

func fileAllowedByViewerScopeCtx(ctx context.Context, filter catalog.AccessFilter, file *models.MediaFile) bool {
	if _, ok := access.GetScope(ctx); !ok {
		return false
	}
	return fileAllowedByViewerScope(filter, file)
}

func fileAllowedByViewerScope(filter catalog.AccessFilter, file *models.MediaFile) bool {
	// A transcode may serve a lower resolution than its source. Admission owns
	// quality decisions; this delivery guard only rechecks library membership.
	return catalog.FileAllowedByLibraryScope(file, filter.AllowedLibraryIDs, filter.DisabledLibraryIDs)
}

func playbackLibraryAllowsSource(r *http.Request, resolver FilePathResolver, fileID int) bool {
	return sourceAllowedByViewerScope(r.Context(), requestAccessFilter(r), resolver, fileID)
}

func playbackLibraryAllowsSourceCtx(ctx context.Context, resolver FilePathResolver, fileID int) bool {
	return sourceAllowedByViewerScope(ctx, AccessFilterFromContext(ctx, ""), resolver, fileID)
}

func sourceAllowedByViewerScope(ctx context.Context, filter catalog.AccessFilter, resolver FilePathResolver, fileID int) bool {
	if _, ok := access.GetScope(ctx); !ok {
		return false
	}
	if filter.AllowedLibraryIDs == nil && len(filter.DisabledLibraryIDs) == 0 {
		return true
	}
	if resolver == nil {
		return false
	}
	file, err := resolver.GetByID(ctx, fileID)
	return err == nil && fileAllowedByViewerScope(filter, file)
}
