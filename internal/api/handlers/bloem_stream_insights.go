package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloemgeo"
	"github.com/Silo-Server/silo-server/internal/bloeminsights"
)

// StreamInsightsCapabilityToken says this server serves the platform stream
// insights routes (docs/architecture/bloem-stream-insights.md).
const StreamInsightsCapabilityToken = "admin_stream_insights_v1"

// streamInsightsRetentionDays is how long stream records and refusals are kept.
const streamInsightsRetentionDays = 90

// streamInsightsTrailLimit caps one trails answer.
const streamInsightsTrailLimit = 2000

// streamInsightsLivePage and streamInsightsLivePages bound one read of the
// live sessions.
const (
	streamInsightsLivePage  = 200
	streamInsightsLivePages = 10
)

// BloemStreamInsightsConfig is what the stream insights routes read.
type BloemStreamInsightsConfig struct {
	Store *bloeminsights.Store
	// Facts are the live streams' request facts; nil leaves live transport
	// to the stream records.
	Facts bloeminsights.FactsStore
	// Geo resolves a viewer address no record has placed yet; nil means no
	// location database.
	Geo *bloemgeo.Resolver
	// Sessions reads the live admin sessions; *AdminHandler.
	Sessions SupportStreamSessions
	Limits   bloeminsights.LimitsFunc
	// Positions places the server and its nodes; nil places neither.
	Positions func(context.Context) bloeminsights.Positions
	// SiloHistory is Silo's Insights definitions, which the viewing stats
	// count by; bloemSiloHistory() when empty.
	SiloHistory bloeminsights.SiloHistory
	Now         func() time.Time
}

// BloemStreamInsightsHandler serves stream insights to the Admin apps:
// where streams come from and how they are connected (platform admins
// only), limit use and refusals, and viewing stats.
type BloemStreamInsightsHandler struct {
	cfg BloemStreamInsightsConfig
}

// NewBloemStreamInsightsHandler returns nil without a store.
func NewBloemStreamInsightsHandler(cfg BloemStreamInsightsConfig) *BloemStreamInsightsHandler {
	if cfg.Store == nil {
		return nil
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.SiloHistory.Viewings == nil {
		cfg.SiloHistory = bloemSiloHistory()
	}
	return &BloemStreamInsightsHandler{cfg: cfg}
}

// Available reports whether the routes can be mounted: NewBloemStreamInsightsHandler
// returns nil without a store.
func (h *BloemStreamInsightsHandler) Available() bool { return h != nil }

// MountPlatform mounts the platform routes on r, a router whose requests
// carry admin context claims.
func (h *BloemStreamInsightsHandler) MountPlatform(r chi.Router) {
	if !h.Available() {
		return
	}
	r.Get("/platform/stream-insights/capabilities", h.HandleCapabilities)
	r.Get("/platform/streams/insights", h.HandleLiveInsights)
	r.Get("/platform/streams/trails", h.HandleTrails)
	r.Get("/platform/streams/transport", h.HandleTransport)
	r.Get("/platform/limits/usage", h.platform(h.handleUsage))
	r.Get("/platform/limits/refusals", h.platform(h.handleRefusals))
}

// requirePlatform accepts only a platform admin context, as the platform
// organization routes do.
func requireStreamInsightsPlatform(w http.ResponseWriter, r *http.Request) bool {
	claims, ok := middleware.GetAdminContextClaims(r.Context())
	if !ok || claims.Scope != auth.AdminScopePlatform || claims.AccountID <= 0 {
		writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
		return false
	}
	return true
}

type scopedInsightsHandler func(w http.ResponseWriter, r *http.Request, scope bloeminsights.Scope)

func (h *BloemStreamInsightsHandler) platform(next scopedInsightsHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireStreamInsightsPlatform(w, r) {
			return
		}
		next(w, r, bloeminsights.Scope{})
	}
}

// BloemStreamInsightsCapabilities says what this server's stream insights can show.
type BloemStreamInsightsCapabilities struct {
	Available     bool   `json:"available"`
	Locations     bool   `json:"locations" doc:"Whether viewer locations resolve: false without a GeoLite2-City database."`
	EdgeHeader    string `json:"edge_header" doc:"The header a Bloem edge adds to every request it forwards, accepted only from a trusted proxy."`
	RetentionDays int    `json:"retention_days"`
}

// HandleCapabilities handles GET /admin/platform/stream-insights/capabilities.
func (h *BloemStreamInsightsHandler) HandleCapabilities(w http.ResponseWriter, r *http.Request) {
	if !requireStreamInsightsPlatform(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, BloemStreamInsightsCapabilities{
		Available: true, Locations: h.cfg.Geo.Available(), EdgeHeader: bloeminsights.EdgeHeader, RetentionDays: streamInsightsRetentionDays,
	})
}

// BloemStreamViewer is where a stream's viewer probably is.
type BloemStreamViewer struct {
	City        string  `json:"city"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Approximate bool    `json:"approximate"`
}

// BloemStreamCloudflare is the Cloudflare colo a stream passed.
type BloemStreamCloudflare struct {
	Colo string   `json:"colo"`
	City string   `json:"city,omitempty"`
	Lat  *float64 `json:"lat,omitempty"`
	Lon  *float64 `json:"lon,omitempty"`
}

// BloemStreamEdge is the Bloem edge that forwarded a stream.
type BloemStreamEdge struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// BloemStreamPlace is the server or the node serving a stream.
type BloemStreamPlace struct {
	Label string  `json:"label"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
}

// BloemStreamTransport is how a stream is connected.
type BloemStreamTransport struct {
	Viewer      string `json:"viewer" enum:"udp,tcp,cloudflare,unknown" doc:"The viewer's leg: udp (HTTP/3 or direct UDP), tcp (HTTP/1.1 or HTTP/2), cloudflare (the viewer reached Cloudflare) or unknown."`
	ViewerProto string `json:"viewer_proto,omitempty"`
	ServerProto string `json:"server_proto,omitempty" doc:"The protocol requests reached this server with."`
	Changed     bool   `json:"changed" doc:"The stream's requests came over more than one class."`
}

// BloemStreamInsight is one live stream's route, connection and limits.
type BloemStreamInsight struct {
	SessionID  string                     `json:"session_id"`
	Viewer     *BloemStreamViewer         `json:"viewer"`
	Cloudflare *BloemStreamCloudflare     `json:"cloudflare"`
	Edge       *BloemStreamEdge           `json:"edge"`
	Server     *BloemStreamPlace          `json:"server"`
	Node       *BloemStreamPlace          `json:"node"`
	Transport  BloemStreamTransport       `json:"transport"`
	Limits     *bloeminsights.PersonUsage `json:"limits"`
}

// BloemStreamInsights lists the live streams.
type BloemStreamInsights struct {
	Items []BloemStreamInsight `json:"items"`
}

// liveSessions reads every live session in scope, a page at a time.
func (h *BloemStreamInsightsHandler) liveSessions(ctx context.Context, scope bloeminsights.Scope) ([]AdminPlaybackSessionView, error) {
	if h.cfg.Sessions == nil {
		return nil, errors.New("live sessions unavailable")
	}
	var out []AdminPlaybackSessionView
	after := ""
	for range streamInsightsLivePages {
		rows, err := h.cfg.Sessions.ReadAdminPlaybackSessions(ctx, PlaybackSessionsQuery{OrganizationID: scope.OrganizationID}, after, streamInsightsLivePage)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(rows) < streamInsightsLivePage {
			break
		}
		after = rows[len(rows)-1].SessionID
	}
	return out, nil
}

func (h *BloemStreamInsightsHandler) usage(ctx context.Context, rows []AdminPlaybackSessionView) ([]bloeminsights.PersonUsage, error) {
	live := make([]bloeminsights.LiveSession, 0, len(rows))
	for _, row := range rows {
		live = append(live, bloeminsights.LiveSession{UserID: row.UserID, Username: row.Username, EffectivePlayMethod: row.EffectivePlayMethod})
	}
	if h.cfg.Limits == nil {
		return []bloeminsights.PersonUsage{}, nil
	}
	return bloeminsights.ComputeUsage(ctx, live, h.cfg.Limits)
}

func (h *BloemStreamInsightsHandler) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "stream insights could not be read", "component", "api", "error", err)
	writeError(w, http.StatusServiceUnavailable, "stream_insights_unavailable", "Stream insights are unavailable")
}

// HandleLiveInsights handles GET /admin/platform/streams/insights.
func (h *BloemStreamInsightsHandler) HandleLiveInsights(w http.ResponseWriter, r *http.Request) {
	if !requireStreamInsightsPlatform(w, r) {
		return
	}
	ctx := r.Context()
	rows, err := h.liveSessions(ctx, bloeminsights.Scope{})
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.SessionID)
	}
	records, err := h.cfg.Store.Records(ctx, ids)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	people, err := h.usage(ctx, rows)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	limits := map[int]*bloeminsights.PersonUsage{}
	for i := range people {
		limits[people[i].UserID] = &people[i]
	}
	var positions bloeminsights.Positions
	if h.cfg.Positions != nil {
		positions = h.cfg.Positions(ctx)
	}
	out := BloemStreamInsights{Items: make([]BloemStreamInsight, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, h.insight(ctx, row, records[row.SessionID], positions, limits[row.UserID]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *BloemStreamInsightsHandler) insight(ctx context.Context, row AdminPlaybackSessionView, record bloeminsights.RecordView,
	positions bloeminsights.Positions, limits *bloeminsights.PersonUsage) BloemStreamInsight {
	item := BloemStreamInsight{SessionID: row.SessionID, Limits: limits, Viewer: h.viewer(row, record),
		Transport: BloemStreamTransport{Viewer: string(bloeminsights.TransportUnknown)}}
	colo, edge := record.CFColo, recordEdge(record)
	if record.Transport != "" {
		item.Transport = BloemStreamTransport{Viewer: string(record.Transport), ViewerProto: record.ViewerProto, ServerProto: record.ServerProto, Changed: record.TransportChanged}
	}
	// A live stream shows its latest request's class.
	if summary, ok := h.liveFacts(ctx, row.SessionID); ok {
		item.Transport = liveTransport(summary)
		if summary.Latest.Edge != nil {
			edge = summary.Latest.Edge
		}
		if summary.Latest.CFColo != "" {
			colo = summary.Latest.CFColo
		}
	}
	item.Cloudflare = cloudflareHop(colo)
	if edge != nil {
		item.Edge = &BloemStreamEdge{Name: edge.Name, Lat: edge.Latitude, Lon: edge.Longitude}
	}
	if positions.Server != nil {
		item.Server = &BloemStreamPlace{Label: positions.Server.Label, Lat: positions.Server.Latitude, Lon: positions.Server.Longitude}
	}
	if nodeID := streamNodeID(row, record); nodeID != nil {
		if node, ok := positions.Nodes[*nodeID]; ok {
			item.Node = &BloemStreamPlace{Label: node.Label, Lat: node.Latitude, Lon: node.Longitude}
		}
	}
	return item
}

// viewer is the record's viewer location, or, before the record has one,
// the live session address's (looked up, never stored).
func (h *BloemStreamInsightsHandler) viewer(row AdminPlaybackSessionView, record bloeminsights.RecordView) *BloemStreamViewer {
	if record.ViewerLat != nil && record.ViewerLon != nil {
		return &BloemStreamViewer{City: record.ViewerCity, Country: record.ViewerCountry, CountryCode: record.ViewerCountryCode,
			Lat: *record.ViewerLat, Lon: *record.ViewerLon, Approximate: record.ViewerApprox}
	}
	if location := h.cfg.Geo.Lookup(row.ClientIP); location != nil {
		return &BloemStreamViewer{City: location.City, Country: location.Country, CountryCode: location.CountryCode,
			Lat: location.Latitude, Lon: location.Longitude, Approximate: location.Approximate}
	}
	return nil
}

func recordEdge(record bloeminsights.RecordView) *bloeminsights.EdgeHop {
	if record.EdgeLat == nil || record.EdgeLon == nil {
		return nil
	}
	return &bloeminsights.EdgeHop{Name: record.EdgeName, Latitude: *record.EdgeLat, Longitude: *record.EdgeLon}
}

func (h *BloemStreamInsightsHandler) liveFacts(ctx context.Context, sessionID string) (bloeminsights.FactsSummary, bool) {
	if h.cfg.Facts == nil {
		return bloeminsights.FactsSummary{}, false
	}
	summary, ok, err := h.cfg.Facts.Summary(ctx, sessionID)
	return summary, ok && err == nil
}

func liveTransport(summary bloeminsights.FactsSummary) BloemStreamTransport {
	latest := summary.Latest
	out := BloemStreamTransport{Viewer: string(bloeminsights.ClassifyViewerLeg(latest)), ViewerProto: latest.Proto,
		ServerProto: latest.Proto, Changed: summary.Changed}
	if latest.Edge != nil && latest.Edge.ViewerProto != "" {
		out.ViewerProto = latest.Edge.ViewerProto
	}
	return out
}

func cloudflareHop(colo string) *BloemStreamCloudflare {
	if colo == "" {
		return nil
	}
	hop := &BloemStreamCloudflare{Colo: colo}
	if city, lat, lon, ok := bloeminsights.ColoLocation(colo); ok {
		hop.City, hop.Lat, hop.Lon = city, &lat, &lon
	}
	return hop
}

func streamNodeID(row AdminPlaybackSessionView, record bloeminsights.RecordView) *int {
	switch {
	case row.RoutingExecutionNodeID != nil:
		return row.RoutingExecutionNodeID
	case row.RoutingEgressNodeID != nil:
		return row.RoutingEgressNodeID
	}
	return record.NodeID
}

func insightsWindow(w http.ResponseWriter, r *http.Request) (bloeminsights.Window, bool) {
	window, err := bloeminsights.ParseWindow(r.URL.Query().Get("window"))
	if err != nil {
		writeAdminValidation(w, map[string]string{"window": "must be live, 24h or 7d"})
		return "", false
	}
	return window, true
}

func insightsDays(w http.ResponseWriter, r *http.Request, def int) (int, bool) {
	days, err := bloeminsights.ParseDays(r.URL.Query().Get("days"), def)
	if err != nil {
		writeAdminValidation(w, map[string]string{"days": "must be between 1 and 90"})
		return 0, false
	}
	return days, true
}

// BloemStreamTrails lists streams' routes.
type BloemStreamTrails struct {
	Trails    []bloeminsights.Trail `json:"trails"`
	Truncated bool                  `json:"truncated" doc:"More streams than the 2000 listed were in the window."`
}

// HandleTrails handles GET /admin/platform/streams/trails.
func (h *BloemStreamInsightsHandler) HandleTrails(w http.ResponseWriter, r *http.Request) {
	if !requireStreamInsightsPlatform(w, r) {
		return
	}
	window, ok := insightsWindow(w, r)
	if !ok {
		return
	}
	var positions bloeminsights.Positions
	if h.cfg.Positions != nil {
		positions = h.cfg.Positions(r.Context())
	}
	trails, truncated, err := h.cfg.Store.Trails(r.Context(), window, h.cfg.Now(), positions, streamInsightsTrailLimit)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	if trails == nil {
		trails = []bloeminsights.Trail{}
	}
	writeJSON(w, http.StatusOK, BloemStreamTrails{Trails: trails, Truncated: truncated})
}

// HandleTransport handles GET /admin/platform/streams/transport.
func (h *BloemStreamInsightsHandler) HandleTransport(w http.ResponseWriter, r *http.Request) {
	if !requireStreamInsightsPlatform(w, r) {
		return
	}
	window, ok := insightsWindow(w, r)
	if !ok {
		return
	}
	summary, err := h.cfg.Store.Transport(r.Context(), window, h.cfg.Now())
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// BloemLimitsUsage is each live person's use against their limits.
type BloemLimitsUsage struct {
	People []bloeminsights.PersonUsage `json:"people"`
}

func (h *BloemStreamInsightsHandler) handleUsage(w http.ResponseWriter, r *http.Request, scope bloeminsights.Scope) {
	rows, err := h.liveSessions(r.Context(), scope)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	people, err := h.usage(r.Context(), rows)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BloemLimitsUsage{People: people})
}

// BloemLimitRefusals is a page of refusals at a limit.
type BloemLimitRefusals struct {
	Items      []bloeminsights.Refusal `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

func (h *BloemStreamInsightsHandler) handleRefusals(w http.ResponseWriter, r *http.Request, scope bloeminsights.Scope) {
	days, ok := insightsDays(w, r, 7)
	if !ok {
		return
	}
	items, next, err := h.cfg.Store.Refusals(r.Context(), scope, days, h.cfg.Now(), r.URL.Query().Get("cursor"), 100)
	if errors.Is(err, bloeminsights.ErrInvalidQuery) {
		writeAdminValidation(w, map[string]string{"cursor": "is invalid"})
		return
	}
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BloemLimitRefusals{Items: items, NextCursor: next})
}

// BloemTopPeople ranks people by watch time.
type BloemTopPeople struct {
	People []bloeminsights.TopPerson `json:"people"`
}

func (h *BloemStreamInsightsHandler) handleTopPeople(w http.ResponseWriter, r *http.Request, scope bloeminsights.Scope) {
	days, ok := insightsDays(w, r, 30)
	if !ok {
		return
	}
	people, err := h.cfg.Store.TopPeople(r.Context(), scope, days, h.cfg.Now(), h.cfg.SiloHistory)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BloemTopPeople{People: people})
}

// BloemPlayHeatmap counts plays by the weekday (0 is Monday, as in Silo's
// Insights) and hour they started, in TimeZone.
type BloemPlayHeatmap struct {
	TimeZone string    `json:"time_zone"`
	Days     int       `json:"days"`
	Plays    [][]int64 `json:"plays" doc:"Seven rows, Monday first, of 24 hourly counts of plays, as Silo's Insights counts them."`
}

func (h *BloemStreamInsightsHandler) handleHeatmap(w http.ResponseWriter, r *http.Request, scope bloeminsights.Scope) {
	days, ok := insightsDays(w, r, 30)
	if !ok {
		return
	}
	zone := strings.TrimSpace(r.URL.Query().Get("tz"))
	if zone == "" {
		zone = "UTC"
	}
	location, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		writeAdminValidation(w, map[string]string{"tz": "must be an IANA time zone"})
		return
	}
	matrix, err := h.cfg.Store.Heatmap(r.Context(), scope, days, h.cfg.Now(), location, h.cfg.SiloHistory)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	out := BloemPlayHeatmap{TimeZone: zone, Days: days, Plays: make([][]int64, 7)}
	for day := range matrix {
		out.Plays[day] = matrix[day][:]
	}
	writeJSON(w, http.StatusOK, out)
}

// BloemClientStats counts sessions per client.
type BloemClientStats struct {
	Clients []bloeminsights.ClientStat `json:"clients"`
}

func (h *BloemStreamInsightsHandler) handleClients(w http.ResponseWriter, r *http.Request, scope bloeminsights.Scope) {
	days, ok := insightsDays(w, r, 30)
	if !ok {
		return
	}
	clients, err := h.cfg.Store.Clients(r.Context(), scope, days, h.cfg.Now(), h.cfg.SiloHistory)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BloemClientStats{Clients: clients})
}

// BloemTranscodeCauses counts why sessions were not played directly.
type BloemTranscodeCauses struct {
	Causes []bloeminsights.TranscodeCause `json:"causes"`
}

func (h *BloemStreamInsightsHandler) handleTranscodeCauses(w http.ResponseWriter, r *http.Request, scope bloeminsights.Scope) {
	days, ok := insightsDays(w, r, 30)
	if !ok {
		return
	}
	causes, err := h.cfg.Store.TranscodeCauses(r.Context(), scope, days, h.cfg.Now(), h.cfg.SiloHistory)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BloemTranscodeCauses{Causes: causes})
}
