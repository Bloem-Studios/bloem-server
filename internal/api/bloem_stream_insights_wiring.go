package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemgeo"
	"github.com/Silo-Server/silo-server/internal/bloeminsights"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// Stream insights timing (docs/architecture/bloem-stream-insights.md).
const (
	streamInsightsFactsTTL   = 6 * time.Hour
	streamInsightsPruneEvery = 24 * time.Hour
	streamInsightsKeep       = 90 * 24 * time.Hour
)

// configureBloemStreamInsights starts stream insights on a process with a
// database: the observer that keeps a record per playback session, its
// daily prune, the media request hook, and the limit refusal recorder. It
// leaves deps.StreamInsights for the router. Without a database it does
// nothing, and the routes stay unmounted.
func configureBloemStreamInsights(deps *Dependencies) {
	if deps == nil || deps.DB == nil || deps.AppContext == nil {
		return
	}
	ctx := deps.AppContext
	logger := slog.Default()
	store := bloeminsights.NewStore(deps.DB)
	var facts bloeminsights.FactsStore
	if deps.RedisClient != nil {
		facts = bloeminsights.NewRedisFactsStore(deps.RedisClient, streamInsightsFactsTTL)
	} else {
		facts = bloeminsights.NewMemoryFactsStore(streamInsightsFactsTTL, nil)
	}
	geo := bloemgeo.OpenFromEnv(logger)
	observer := bloeminsights.NewObserver(store, geo, facts, logger)
	if deps.EventsHub != nil {
		go observer.Run(ctx, deps.EventsHub)
	}
	go observer.PruneLoop(ctx, streamInsightsPruneEvery, streamInsightsKeep)

	secret := ""
	if cfg := deps.CurrentConfig(); cfg != nil {
		secret = cfg.Auth.JWTSecret
	}
	SetStreamInsightsObserver(func(sessionID string, f bloeminsights.RequestFacts) {
		if err := facts.Record(ctx, sessionID, f); err != nil && ctx.Err() == nil {
			logger.Warn("stream insights could not record request facts", "session", sessionID, "error", err)
		}
	}, deps.ClientIPResolver, secret)

	var notifier bloeminsights.LimitNotifier
	if push := deps.Notifications.BloemAdminPush(); push != nil {
		notifier = push
	}
	var limits bloeminsights.RefusalLimitsFunc
	if deps.SessionMgr != nil {
		sessions := deps.SessionMgr
		limits = func(ctx context.Context, userID int, _ string) (playback.SessionLimits, error) {
			return sessions.LimitsForUser(ctx, userID)
		}
	}
	refusals := bloeminsights.NewRefusalRecorder(deps.DB, notifier, limits, logger)
	go refusals.Run(ctx)
	if deps.SessionMgr != nil {
		deps.SessionMgr.SetLimitRefusalObserver(refusals)
	}
	playback.SetAdmissionRefusalObserver(refusals)

	deps.StreamInsights = &bloeminsights.Runtime{Store: store, Facts: facts, Geo: geo, Observer: observer, Refusals: refusals}
	slog.Info("bloem stream insights started", "locations", geo.Available(), "redis_facts", deps.RedisClient != nil)
}
