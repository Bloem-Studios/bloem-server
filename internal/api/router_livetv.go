package api

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/go-chi/chi/v5"
)

// mountLiveTVRoutes runs inside the authenticated, resolved-viewer group.
func mountLiveTVRoutes(r chi.Router, liveTVHandler *handlers.LiveTVHandler, requireActingAdmin func(http.Handler) http.Handler) {
	if liveTVHandler == nil {
		return
	}
	r.Route("/livetv", func(r chi.Router) {
		r.With(apimw.RequireLiveTVAccess).Get("/channels", liveTVHandler.HandleListChannels)
		r.With(apimw.RequireLiveTVAccess).Get("/guide", liveTVHandler.HandleListGuide)
		r.With(apimw.RequireLiveTVAccess).Get("/programs/{programId}", liveTVHandler.HandleGetProgram)
		r.With(apimw.RequireLiveTVAccess).Get("/recordings", liveTVHandler.HandleListRecordings)
		r.With(apimw.RequireLiveTVAccess).Get("/series-rules", liveTVHandler.HandleListSeriesRules)

		r.Group(func(r chi.Router) {
			r.Use(apimw.RequireProfile)
			r.With(apimw.RequireLiveTVAccess).Post("/channels/{channelId}/session", liveTVHandler.HandleStartChannelSession)
			r.With(apimw.RequireLiveTVStreamAccess).Get("/sessions/{sessionId}/stream", liveTVHandler.HandleSessionStream)
			r.With(apimw.RequireLiveTVStreamAccess).Method(http.MethodHead, "/sessions/{sessionId}/stream", http.HandlerFunc(liveTVHandler.HandleSessionStream))
			r.With(apimw.RequireLiveTVStreamAccess).Get("/live-hls/{playbackId}/{name}", liveTVHandler.HandleLiveHLS)
			r.With(apimw.RequireLiveTVAccess).Post("/sessions/{sessionId}/heartbeat", liveTVHandler.HandleSessionHeartbeat)
			r.Delete("/sessions/{sessionId}", liveTVHandler.HandleReleaseSession)
			r.With(apimw.RequireLiveTVAccess).Post("/recordings", liveTVHandler.HandleScheduleRecording)
			r.With(apimw.RequireLiveTVAccess).Delete("/recordings/{recordingId}", liveTVHandler.HandleCancelRecording)
			r.With(apimw.RequireLiveTVAccess).Post("/series-rules", liveTVHandler.HandleCreateSeriesRule)
			r.With(apimw.RequireLiveTVAccess).Delete("/series-rules/{ruleId}", liveTVHandler.HandleDeleteSeriesRule)
		})

		r.Group(func(r chi.Router) {
			r.Use(requireActingAdmin)
			r.Get("/tuners", liveTVHandler.HandleListTuners)
			r.Post("/tuners/discover", liveTVHandler.HandleDiscoverTuners)
			r.Post("/tuners", liveTVHandler.HandleAddTuner)
			r.Delete("/tuners/{tunerId}", liveTVHandler.HandleDeleteTuner)
			r.Post("/tuners/{tunerId}/scan", liveTVHandler.HandleScanTuner)
			r.Patch("/channels/{channelId}", liveTVHandler.HandlePatchChannel)
			r.Get("/guide-sources", liveTVHandler.HandleListGuideSources)
			r.Post("/guide-sources/schedules-direct/lineups", liveTVHandler.HandleLookupSchedulesDirectLineups)
			r.Post("/guide-sources/xml-sync/lineups", liveTVHandler.HandleLookupXMLSyncLineups)
			r.Post("/guide-sources", liveTVHandler.HandleCreateGuideSource)
			r.Patch("/guide-sources/{sourceId}", liveTVHandler.HandleUpdateGuideSource)
			r.Delete("/guide-sources/{sourceId}", liveTVHandler.HandleDeleteGuideSource)
			r.Post("/guide-sources/{sourceId}/sync", liveTVHandler.HandleSyncGuideSource)
		})
	})
}
