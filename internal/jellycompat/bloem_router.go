package jellycompat

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerLiveTVRoutes wires the optional Live TV service into the compat
// handlers and registers its routes. A nil deps.LiveTV registers nothing.
func registerLiveTVRoutes(r chi.Router, deps Dependencies, authHandler *AuthHandler, itemsHandler *ItemsHandler, playbackHandler *PlaybackHandler) {
	if deps.LiveTV == nil {
		return
	}
	liveTVHandler := NewLiveTVHandler(deps.LiveTV, deps.IDCodec, deps.Config)
	liveTVHandler.access = deps.LiveTVAccessFn
	authHandler.liveTVAccess = deps.LiveTVAccessFn
	authHandler.SetLiveTVEnabled(true)
	itemsHandler.SetLiveTV(liveTVHandler)
	playbackHandler.SetLiveTV(liveTVHandler)
	r.Get("/LiveTv/Info", liveTVHandler.HandleInfo)
	r.Get("/LiveTv/GuideInfo", liveTVHandler.HandleGuideInfo)
	r.Get("/LiveTv/Channels", liveTVHandler.HandleChannels)
	r.Get("/LiveTv/Channels/{id}", liveTVHandler.HandleChannel)
	r.Get("/LiveTv/Programs", liveTVHandler.HandlePrograms)
	r.Post("/LiveTv/Programs", liveTVHandler.HandlePrograms)
	r.Get("/LiveTv/Programs/Recommended", liveTVHandler.HandleRecommendedPrograms)
	r.Get("/LiveTv/Programs/{id}", liveTVHandler.HandleProgram)
	r.Get("/LiveTv/Timers", liveTVHandler.HandleTimers)
	r.Post("/LiveTv/Timers", liveTVHandler.HandleTimers)
	r.Get("/LiveTv/Timers/{id}", liveTVHandler.HandleTimer)
	r.Post("/LiveTv/Timers/{id}", liveTVHandler.HandleTimer)
	r.Delete("/LiveTv/Timers/{id}", liveTVHandler.HandleTimer)
	r.Get("/LiveTv/SeriesTimers", liveTVHandler.HandleSeriesTimers)
	r.Post("/LiveTv/SeriesTimers", liveTVHandler.HandleSeriesTimers)
	r.Get("/LiveTv/SeriesTimers/{id}", liveTVHandler.HandleSeriesTimer)
	r.Post("/LiveTv/SeriesTimers/{id}", liveTVHandler.HandleSeriesTimer)
	r.Delete("/LiveTv/SeriesTimers/{id}", liveTVHandler.HandleSeriesTimer)
	r.Get("/LiveTv/Recordings", liveTVHandler.HandleRecordings)
	r.Post("/LiveStreams/Open", liveTVHandler.HandleOpenLiveStream)
	r.Post("/LiveStreams/Close", liveTVHandler.HandleCloseLiveStream)
	r.Method(http.MethodHead, "/LiveTv/LiveStreamFiles/{id}/stream", http.HandlerFunc(liveTVHandler.HandleLiveStreamFile))
	r.Get("/LiveTv/LiveStreamFiles/{id}/stream", liveTVHandler.HandleLiveStreamFile)
	r.Method(http.MethodHead, "/LiveTv/LiveStreamFiles/{id}/stream.{container}", http.HandlerFunc(liveTVHandler.HandleLiveStreamFile))
	r.Get("/LiveTv/LiveStreamFiles/{id}/stream.{container}", liveTVHandler.HandleLiveStreamFile)
}
