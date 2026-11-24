package apiv2

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/bloeminsights"
)

// Stream insights (handlers.BloemStreamInsightsHandler,
// docs/architecture/bloem-stream-insights.md): where streams come from and
// how they are connected, for platform admins; limit use, refusals and
// viewing stats. Servers that serve them list admin_stream_insights_v1.

type BloemStreamInsightsCapabilitiesOutput struct {
	Body handlers.BloemStreamInsightsCapabilities
}

type BloemStreamInsightsOutput struct {
	Body handlers.BloemStreamInsights
}

type BloemStreamWindowInput struct {
	Window string `query:"window" enum:"live,24h,7d" doc:"live (default): the streams playing now; 24h or 7d: those started in that time."`
}

type BloemStreamTrailsOutput struct {
	Body handlers.BloemStreamTrails
}

type BloemStreamTransportOutput struct {
	Body bloeminsights.TransportSummary
}

type BloemLimitsUsageOutput struct {
	Body handlers.BloemLimitsUsage
}

type BloemStatsDaysInput struct {
	Days int `query:"days" minimum:"1" maximum:"90" doc:"How many days back (default 30; 7 for refusals)."`
}

type BloemLimitRefusalsInput struct {
	Days   int    `query:"days" minimum:"1" maximum:"90" doc:"How many days back (default 7)."`
	Cursor string `query:"cursor" doc:"next_cursor from the previous page."`
}

type BloemLimitRefusalsOutput struct {
	Body handlers.BloemLimitRefusals
}

type BloemTopPeopleOutput struct {
	Body handlers.BloemTopPeople
}

type BloemPlayHeatmapInput struct {
	Days int    `query:"days" minimum:"1" maximum:"90" doc:"How many days back (default 30)."`
	TZ   string `query:"tz" doc:"IANA time zone the weekday and hour are read in (default UTC)."`
}

type BloemPlayHeatmapOutput struct {
	Body handlers.BloemPlayHeatmap
}

type BloemClientStatsOutput struct {
	Body handlers.BloemClientStats
}

type BloemTranscodeCausesOutput struct {
	Body handlers.BloemTranscodeCauses
}

const bloemStreamInsightsPlatform = "Requires a Platform administrative context; organization contexts are refused with insufficient_platform_authority. "

func bloemStreamInsightsOp(reg *Registry, path, id, summary, description string, validation string) huma.Operation {
	return bloemStreamInsightsScopedOp(reg, path, id, summary, bloemStreamInsightsPlatform+description, validation, false)
}

const bloemStreamInsightsOrganization = "Requires an organization administrative context; Platform contexts are refused with insufficient_organization_authority. Counts only the organization's members and never carries a location, edge, Cloudflare colo or transport. Servers that serve it list admin_org_stream_limits_v1. "

func bloemStreamInsightsScopedOp(reg *Registry, path, id, summary, description string, validation string, organization bool) huma.Operation {
	security, forbidden := "bloemPlatformContext", "insufficient_platform_authority: the context is not a Platform context."
	if organization {
		security, forbidden = "bloemOrganizationContext", "insufficient_organization_authority: the context is not this organization's administrative context."
	}
	op := bloemChiDocumentOp(reg, "GET", path, id, "stream-insights", summary, security, http.StatusOK)
	op.Description = description
	op.Responses = map[string]*huma.Response{}
	errors := map[int]string{
		401: "tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current.",
		403: forbidden,
		503: "stream_insights_unavailable: the database or the live session list is unavailable.",
	}
	if validation != "" {
		errors[422] = "validation_failed: " + validation
	}
	bloemDocumentErrors[BloemNativeError](reg, &op, errors)
	op.Responses["200"] = &huma.Response{Description: http.StatusText(http.StatusOK)}
	return op
}

// registerBloemStreamInsightsDocument documents the platform stream insights routes.
func registerBloemStreamInsightsDocument(reg *Registry) {
	registerBloemChiDocument[struct{}, BloemStreamInsightsCapabilitiesOutput](reg, bloemStreamInsightsOp(reg,
		"/admin/platform/stream-insights/capabilities", "getBloemStreamInsightsCapabilities", "Say what stream insights can show.",
		"locations is false without a GeoLite2-City database: every route still answers, with no viewer locations.", ""))
	registerBloemChiDocument[struct{}, BloemStreamInsightsOutput](reg, bloemStreamInsightsOp(reg,
		"/admin/platform/streams/insights", "listBloemLiveStreamInsights", "List the live streams' routes, connections and limits.",
		"One item per live stream: the viewer's probable location (from the address, which is never stored), the Cloudflare colo and Bloem edge it passed (counted only from a trusted proxy), the server and node serving it (from the bloem.map.locations setting), how the viewer's leg is connected (its latest request), and the person's use of their limits. A hop that is not known is null.", ""))
	registerBloemChiDocument[BloemStreamWindowInput, BloemStreamTrailsOutput](reg, bloemStreamInsightsOp(reg,
		"/admin/platform/streams/trails", "listBloemStreamTrails", "List streams' routes for a map.",
		"Each trail lists, in order, the hops that have coordinates: viewer, cloudflare, edge, server, node. At most 2000 trails, newest first; truncated says more were left out.", "window must be live, 24h or 7d."))
	registerBloemChiDocument[BloemStreamWindowInput, BloemStreamTransportOutput](reg, bloemStreamInsightsOp(reg,
		"/admin/platform/streams/transport", "getBloemStreamTransport", "Count streams by how they are connected.",
		"counts the window's streams by the viewer's leg: udp (HTTP/3 or direct UDP), tcp (HTTP/1.1 or HTTP/2), cloudflare or unknown. A finished stream counts in the class that carried most of its requests. series has hourly buckets for 24h and six-hour buckets for 7d, by start time; it is empty for live.", "window must be live, 24h or 7d."))
	registerBloemChiDocument[struct{}, BloemLimitsUsageOutput](reg, bloemStreamInsightsOp(reg,
		"/admin/platform/limits/usage", "getBloemLimitsUsage", "Show each live person's use of their limits.",
		"A person's own stream and transcode limits, and the household and organization pools they share. A max of 0 is no limit; a pool that does not apply is absent.", ""))
	registerBloemChiDocument[BloemLimitRefusalsInput, BloemLimitRefusalsOutput](reg, bloemStreamInsightsOp(reg,
		"/admin/platform/limits/refusals", "listBloemLimitRefusals", "List refusals at a stream or transcode limit.",
		"Newest first, 100 a page. Repeats of one person's refusal at one limit within a minute are one row.", "days must be between 1 and 90, or the cursor is invalid."))
	registerBloemChiDocument[BloemStatsDaysInput, BloemTopPeopleOutput](reg, bloemStreamInsightsOp(reg,
		"/admin/platform/stats/top-people", "listBloemTopPeople", "Rank people by watch time.",
		"The 50 people who watched most, from the playback history.", "days must be between 1 and 90."))
	registerBloemChiDocument[BloemPlayHeatmapInput, BloemPlayHeatmapOutput](reg, bloemStreamInsightsOp(reg,
		"/admin/platform/stats/heatmap", "getBloemPlayHeatmap", "Count plays by weekday and hour.",
		"Seven rows, Sunday first, of 24 counts: the plays that started at that local weekday and hour.", "days must be between 1 and 90, or tz is not an IANA time zone."))
	registerBloemChiDocument[BloemStatsDaysInput, BloemClientStatsOutput](reg, bloemStreamInsightsOp(reg,
		"/admin/platform/stats/clients", "listBloemClientStats", "Count plays per client.",
		"Plays per client name and platform, and how many of them transcoded video.", "days must be between 1 and 90."))
	registerBloemChiDocument[BloemStatsDaysInput, BloemTranscodeCausesOutput](reg, bloemStreamInsightsOp(reg,
		"/admin/platform/stats/transcode-causes", "listBloemTranscodeCauses", "Count why video was transcoded.",
		"Video transcodes per client and adaptation reason, from the stream records.", "days must be between 1 and 90."))

	org := func(path, id, summary, description, validation string) huma.Operation {
		return bloemStreamInsightsScopedOp(reg, path, id, summary, bloemStreamInsightsOrganization+description, validation, true)
	}
	registerBloemChiDocument[struct{}, BloemLimitsUsageOutput](reg, org("/admin/organization/limits/usage", "getBloemOrganizationLimitsUsage",
		"Show each live member's use of their limits.", "As the Platform route, for the organization's members.", ""))
	registerBloemChiDocument[BloemLimitRefusalsInput, BloemLimitRefusalsOutput](reg, org("/admin/organization/limits/refusals", "listBloemOrganizationLimitRefusals",
		"List the members' refusals at a limit.", "Newest first, 100 a page.", "days must be between 1 and 90, or the cursor is invalid."))
	registerBloemChiDocument[BloemStatsDaysInput, BloemTopPeopleOutput](reg, org("/admin/organization/stats/top-people", "listBloemOrganizationTopPeople",
		"Rank members by watch time.", "The 50 members who watched most.", "days must be between 1 and 90."))
	registerBloemChiDocument[BloemPlayHeatmapInput, BloemPlayHeatmapOutput](reg, org("/admin/organization/stats/heatmap", "getBloemOrganizationPlayHeatmap",
		"Count the members' plays by weekday and hour.", "Seven rows, Sunday first, of 24 counts.", "days must be between 1 and 90, or tz is not an IANA time zone."))
	registerBloemChiDocument[BloemStatsDaysInput, BloemClientStatsOutput](reg, org("/admin/organization/stats/clients", "listBloemOrganizationClientStats",
		"Count the members' plays per client.", "Plays per client name and platform, and how many transcoded video.", "days must be between 1 and 90."))
	registerBloemChiDocument[BloemStatsDaysInput, BloemTranscodeCausesOutput](reg, org("/admin/organization/stats/transcode-causes", "listBloemOrganizationTranscodeCauses",
		"Count why the members' video was transcoded.", "Video transcodes per client and adaptation reason.", "days must be between 1 and 90."))
}
