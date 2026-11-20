package apiv2

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

const bloemThemePlaybackOperation = "createThemeSongPlayback"

// bloemAdminRequestOrganization is the request service's organization check
// for an admin read by id (*requests.Service, bloem_tenant_scope.go).
type bloemAdminRequestOrganization interface {
	BloemRequireAdminRequestOrganization(ctx context.Context, viewer mediarequests.Viewer, id string) error
}

// Run after the host's identity/profile gates and before handlers can mint
// content access. Catalog and watch-detail reads remain available. An admin
// request poster is served only within the acting organization, like every
// other admin request read.
func bloemContentGate(deps Dependencies, ctx huma.Context, next func(huma.Context)) {
	switch ctx.Operation().OperationID {
	case "getWatchTrickplay", bloemThemePlaybackOperation:
		scope, ok := access.GetScope(ctx.Context())
		if !ok || !scope.PlaybackAllowed {
			r, w := humachi.Unwrap(ctx)
			writeProblem(w, r, NewProblem(TypePermissionDenied, "Playback has not been granted."))
			return
		}
	case opGetAdminRequestPoster:
		if s, ok := deps.AdminRequests.(bloemAdminRequestOrganization); ok {
			r, w := humachi.Unwrap(ctx)
			if err := s.BloemRequireAdminRequestOrganization(ctx.Context(), adminRequestViewer(ctx.Context()), chi.URLParam(r, "id")); err != nil {
				w.Header().Set("Cache-Control", "no-store")
				writeProblem(w, r, requestProblem(err))
				return
			}
		}
	}
	next(ctx)
}

// bloemThenContentGate runs bloemContentGate in front of next. classGate
// applies it once its gates admitted the request, which is where the gate
// stood as a Huma middleware of its own.
func bloemThenContentGate(deps Dependencies, next func(huma.Context)) func(huma.Context) {
	return func(ctx huma.Context) { bloemContentGate(deps, ctx, next) }
}
