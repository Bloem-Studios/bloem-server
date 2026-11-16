package api

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/nodecontrol"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// Native inputs on transcode nodes: the router wiring of
// handlers.NativeNodeSources. See
// docs/architecture/bloem-native-video-playback.md#where-native-inputs-run.

// useBloemNativeNodeSources takes Silo's source bytes route for a Bloem
// grant; every other request goes on through Silo's middleware and routes.
func useBloemNativeNodeSources(r chi.Router, deps Dependencies) {
	if deps.NativeNodeSources == nil || deps.NodeKeyRegistry == nil || deps.NodeRepo == nil {
		return
	}
	r.Use(deps.NativeNodeSources.SourceBytesMiddleware(deps.NodeKeyRegistry))
}

// bloemNativeNodeControl wraps the node control routes' grant issuer and
// catalog check: a node's resolve of a native recipe gets a Bloem grant, and
// a Silo grant never opens a native file.
func (d Dependencies) bloemNativeNodeControl(opts *nodecontrol.Options) {
	if d.NativeNodeSources == nil {
		return
	}
	opts.Grants = d.NativeNodeSources.GrantIssuer(opts.Grants)
	opts.Files = d.NativeNodeSources.FileCatalog(opts.Files)
}

// installNativeNodeSources composes the native video host into the node
// source grants once nativeVideoPlayback has built it.
func installNativeNodeSources(deps Dependencies, service *handlers.NativeVideoService, secret func() string) {
	if deps.NativeNodeSources == nil || service == nil || deps.NodeRepo == nil {
		return
	}
	handlers.RegisterNativeCredentialRefusals(middleware.ErrCurrentCredentialInvalid, middleware.ErrCurrentCredentialForbidden)
	cfg := handlers.NativeNodeSourceConfig{Service: service, Secret: secret, Lifetime: deps.AppContext,
		Node: func(ctx context.Context, id int) (*nodepool.Node, error) { return deps.NodeRepo.GetByID(ctx, id) }}
	if deps.RedisClient != nil {
		recipes := noderecipe.NewStore(deps.RedisClient, 0)
		cfg.Recipes = func(ctx context.Context, transportID string) (*playback.RecipeCard, bool) {
			return recipes.Get(ctx, transportID)
		}
	}
	deps.NativeNodeSources.Install(cfg)
}

// bloemNativeSourceGrants is the playback handler's granter: Silo's, with
// native files granted by NativeNodeSources.
func bloemNativeSourceGrants(deps Dependencies, h *handlers.PlaybackHandler) {
	if h != nil && deps.NativeNodeSources != nil {
		h.SourceGrants = deps.NativeNodeSources.Granter(h.SourceGrants)
	}
}
