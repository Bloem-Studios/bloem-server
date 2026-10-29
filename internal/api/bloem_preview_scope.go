package api

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

func narrowBloemPreviewScope(ctx context.Context, pool *pgxpool.Pool, scope access.Scope) (access.Scope, error) {
	if !previewcontext.Active(ctx) {
		return scope, nil
	}
	var libraries []int
	var ceiling string
	err := pool.QueryRow(ctx, `SELECT ARRAY(SELECT f.id FROM media_folders f WHERE f.enabled AND (r.library_ids IS NULL OR f.id=ANY(r.library_ids)) ORDER BY f.id),s.maturity_ceiling
 FROM bloem_preview_settings s JOIN entitlement_templates t ON t.key='bloem-preview'
 JOIN entitlement_template_revisions r ON r.template_key=t.key AND r.revision=t.current_revision`).Scan(&libraries, &ceiling)
	if err != nil {
		return access.Scope{}, err
	}
	allowed := make([]int, 0, len(libraries))
	for _, id := range libraries {
		if scope.AllowedLibraryIDs == nil {
			allowed = append(allowed, id)
			continue
		}
		for _, current := range scope.AllowedLibraryIDs {
			if current == id {
				allowed = append(allowed, id)
				break
			}
		}
	}
	scope.AllowedLibraryIDs = allowed
	scope.LibrariesRestricted = true
	scope.MaxContentRating = access.StricterCeiling(scope.MaxContentRating, ceiling)
	// Only a guest holding a live free-play grant may play (spec: free play);
	// guest admission checked the grant before marking the request.
	_, freePlay := previewcontext.FreePlayGrant(ctx)
	scope.PlaybackAllowed = freePlay
	scope.DownloadAllowed = false
	scope.LiveTVAllowed = false
	return scope, nil
}
