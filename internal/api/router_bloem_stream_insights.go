package api

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/bloeminsights"
	"github.com/Silo-Server/silo-server/internal/serversettings"
)

// bloemStreamInsightsHandler builds the stream insights routes' handler from
// the runtime ConfigureBloemAdminPush started, or nil when there is none (no
// database, or no workers on this process). admin reads the live sessions.
func bloemStreamInsightsHandler(deps Dependencies, admin *handlers.AdminHandler) *handlers.BloemStreamInsightsHandler {
	runtime := deps.StreamInsights
	if runtime == nil || runtime.Store == nil || deps.DB == nil {
		return nil
	}
	cfg := handlers.BloemStreamInsightsConfig{Store: runtime.Store, Facts: runtime.Facts, Geo: runtime.Geo}
	if admin != nil && admin.AdminPlaybackSessionsAvailable() {
		cfg.Sessions = admin
	}
	if deps.SessionMgr != nil {
		cfg.Limits = deps.SessionMgr.LimitsForUser
	}
	settings := serversettings.NewServerSettingsRepo(deps.DB)
	cfg.Positions = func(ctx context.Context) bloeminsights.Positions {
		return bloeminsights.ServerPositions(ctx, settings, runtime.Geo, nil)
	}
	return handlers.NewBloemStreamInsightsHandler(cfg)
}
