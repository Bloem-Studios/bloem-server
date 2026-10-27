package apiv2

import "github.com/Silo-Server/silo-server/internal/livetv"

// Live TV remains served by chi in the native client group. These
// document-only declarations describe its native gates and statuses, without
// the v2 wrapper's Problems or retry headers.
func registerBloemLiveTV(reg *Registry) {
	capability := bloemLiveTVDocumentOp(reg, "GET", "/livetv/capability", "getBloemLiveTVCapability", "Whether this build and viewer support Live TV and a channel is available.", 200, bloemLiveTVUngated)
	capability.Description += " allowed reports whether the selected profile holds Live TV permission; available is computed only when it does, and channel metadata is never returned. Served with Cache-Control: no-store."
	registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.CapabilityResponse]](reg, capability)

	channels := bloemLiveTVDocumentOp(reg, "GET", "/livetv/channels", "listBloemLiveTVChannels", "The channel lineup this viewer may watch.", 200, bloemLiveTVViewer)
	bloemDocumentQuery(&channels, "tuner_id", "Optional tuner filter.", "string")
	registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.ChannelsResponse]](reg, channels)

	guide := bloemLiveTVDocumentOp(reg, "GET", "/livetv/guide", "getBloemLiveTVGuide", "Program guide for a window.", 200, bloemLiveTVViewer)
	bloemDocumentQuery(&guide, "start", "RFC3339; defaults to now.", "string")
	bloemDocumentQuery(&guide, "end", "RFC3339; defaults to start plus twelve hours.", "string")
	bloemDocumentQuery(&guide, "channels", "Comma-separated channel IDs.", "string")
	for _, parameter := range guide.Parameters {
		if parameter.In == "query" && (parameter.Name == "start" || parameter.Name == "end") {
			parameter.Schema.Format = "date-time"
		}
	}
	bloemDocumentErrors[BloemNativeError](reg, &guide, map[int]string{
		400: "bad_request: X-Profile-Id is missing. invalid_start or invalid_end: start or end is not RFC 3339.",
	})
	registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.GuideResponse]](reg, guide)

	session := bloemLiveTVDocumentOp(reg, "POST", "/livetv/channels/{channelId}/session", "startBloemLiveTVSession", "Tune a channel and take a tuner lease.", 201, bloemLiveTVMedia)
	session.Description += " Optional client codec capabilities are read from a body of at most 16 KiB; omit for native broadcast-codec support. Returns 201 and canonical same-origin delivery URLs. HLS URLs carry a session-bound st ticket; MPEG-TS proxy URLs require account/profile authentication. Repeating a start can allocate another lease; do not automatically retry."
	bloemDocumentErrors[BloemNativeError](reg, &session, map[int]string{
		400: "bad_request: X-Profile-Id is missing. invalid_body: the capability body is not valid JSON or exceeds 16 KiB.",
		404: "not_found: the channel does not exist or is disabled, its tuner is gone, unreachable or refused the channel, or the channel has no signal; or X-Profile-Id names no profile of this account.",
		409: "no_tuner: every tuner is in use or unavailable. limit_exceeded: live transcode capacity or the Xtream provider's connection budget is in use.",
	})
	bloemLiveTVUnavailable(&session, "unavailable: Live TV delivery signing is not configured.")
	registerBloemChiDocument[bloemOptionalJSONInput[BloemLiveTVClientCapabilities], bloemJSONOutput[livetv.SessionStartResponse]](reg, session)

	release := bloemLiveTVDocumentOp(reg, "DELETE", "/livetv/sessions/{sessionId}", "releaseBloemLiveTVSession", "Release an owned tuner lease and return the session record.", 200, bloemLiveTVUngated)
	release.Description += " Owned cleanup therefore remains possible after revocation." + bloemLiveTVOwnership + " Returns the serialized session record rather than a bodyless response; releasing an already released session returns it again."
	bloemDocumentErrors[BloemNativeError](reg, &release, map[int]string{
		404: "not_found: the session does not exist or belongs to another viewer, or X-Profile-Id names no profile of this account.",
	})
	registerBloemChiDocument[struct{}, bloemJSONOutput[BloemLiveTVSessionResponse]](reg, release)
}
