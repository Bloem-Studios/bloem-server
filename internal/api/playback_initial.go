package api

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/diagnostics"
	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/playback/planstore"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// NewInitialPlaybackRuntime prepares explicit initial-flow dependencies. It does
// not enroll accounts or start a server. InstallationID must be the persisted
// diagnostics.ServerInstanceID used by progress bootstrap on the same server.
// Application startup deliberately does not call this constructor yet.
func NewInitialPlaybackRuntime(ctx context.Context, pool *pgxpool.Pool, redisClient *redis.Client, sources userstore.PlaybackSourceProvider, installationID string, ownerPolicy, grantPolicy playback.RuntimeGrantPolicyV3) (*handlers.InitialPlaybackFlowV3, error) {
	id, err := uuid.Parse(installationID)
	if err != nil || id == uuid.Nil || id.String() != installationID {
		return nil, errors.New("persisted playback installation identity required")
	}
	if ctx == nil || pool == nil || redisClient == nil || sources == nil {
		return nil, errors.New("initial playback runtime dependencies required")
	}
	persisted, err := diagnostics.ServerInstanceID(ctx, catalog.NewServerSettingsRepo(pool))
	if err != nil {
		return nil, err
	}
	if persisted != installationID {
		return nil, errors.New("playback installation identity differs from persisted server identity")
	}
	if err := ownerPolicy.Validate(); err != nil {
		return nil, err
	}
	if err := grantPolicy.Validate(); err != nil {
		return nil, err
	}
	store, err := planstore.NewPostgresWithGrantPolicy(pool, playback.AttemptGrantPolicyV3{MaxDuration: grantPolicy.MaxDuration})
	if err != nil {
		return nil, err
	}
	clock, err := playback.NewRuntimeGrantClockV3()
	if err != nil {
		return nil, err
	}
	recipes := noderecipe.NewStore(redisClient, playback.MaxTokenTTL)
	runtime, err := planstore.NewExecutorRuntime(store, recipes, 0, clock, grantPolicy)
	if err != nil {
		return nil, err
	}
	return &handlers.InitialPlaybackFlowV3{InstallationID: installationID, Control: store, Sources: sources, Recipes: recipes, OwnerID: uuid.NewString(), Context: ctx, Clock: clock, Policy: ownerPolicy, AcquireGrant: runtime.Acquire, ResolveRecipe: runtime.Resolve}, nil
}
