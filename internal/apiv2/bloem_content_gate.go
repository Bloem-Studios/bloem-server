package apiv2

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"

	"github.com/Silo-Server/silo-server/internal/access"
)

const bloemThemePlaybackOperation = "createThemeSongPlayback"

// Run after the host's identity/profile gates and before handlers can mint
// content access. Catalog and watch-detail reads remain available.
func bloemContentGate(ctx huma.Context, next func(huma.Context)) {
	switch ctx.Operation().OperationID {
	case "getWatchTrickplay", bloemThemePlaybackOperation:
		scope, ok := access.GetScope(ctx.Context())
		if !ok || !scope.PlaybackAllowed {
			r, w := humachi.Unwrap(ctx)
			writeProblem(w, r, NewProblem(TypePermissionDenied, "Playback has not been granted."))
			return
		}
	}
	next(ctx)
}

// bloemThenContentGate runs bloemContentGate in front of next. classGate
// applies it once its gates admitted the request, which is where the gate
// stood as a Huma middleware of its own.
func bloemThenContentGate(next func(huma.Context)) func(huma.Context) {
	return func(ctx huma.Context) { bloemContentGate(ctx, next) }
}
