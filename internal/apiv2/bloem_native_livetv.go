package apiv2

import "github.com/Silo-Server/silo-server/internal/livetv"

// Live TV remains served by chi. These document-only declarations describe its
// native gates and statuses, without the v2 wrapper's Problems or retry headers.
func registerBloemLiveTV(reg *Registry) {
	capability := bloemLiveTVDocumentOp(reg, "GET", "/livetv/capability", "getBloemLiveTVCapability", "Whether this build and viewer support Live TV and a channel is available.", 200, false)
	registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.CapabilityResponse]](reg, capability)

	channels := bloemLiveTVDocumentOp(reg, "GET", "/livetv/channels", "listBloemLiveTVChannels", "The channel lineup this viewer may watch.", 200, false)
	bloemDocumentQuery(&channels, "tuner_id", "Optional tuner filter.", "string")
	registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.ChannelsResponse]](reg, channels)

	guide := bloemLiveTVDocumentOp(reg, "GET", "/livetv/guide", "getBloemLiveTVGuide", "Programme guide for a window.", 200, false)
	bloemDocumentQuery(&guide, "start", "RFC3339; defaults to now.", "string")
	bloemDocumentQuery(&guide, "end", "RFC3339; defaults to start plus twelve hours.", "string")
	bloemDocumentQuery(&guide, "channels", "Comma-separated channel IDs.", "string")
	registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.GuideResponse]](reg, guide)

	session := bloemLiveTVDocumentOp(reg, "POST", "/livetv/channels/{channelId}/session", "startBloemLiveTVSession", "Tune a channel and take a tuner lease.", 201, false)
	session.Description += " Requires both current Live TV and playback permission. Optional client codec capabilities are read from a body of at most 16 KiB; omit for native broadcast-codec support. Returns 201 and canonical same-origin delivery URLs. HLS URLs carry a session-bound st ticket; MPEG-TS proxy URLs require account/profile authentication. Repeating a start can allocate another lease; do not automatically retry."
	registerBloemChiDocument[bloemOptionalJSONInput[BloemLiveTVClientCapabilities], bloemJSONOutput[livetv.SessionStartResponse]](reg, session)

	release := bloemLiveTVDocumentOp(reg, "DELETE", "/livetv/sessions/{sessionId}", "releaseBloemLiveTVSession", "Release an owned tuner lease and return the session record.", 200, false)
	release.Description += " Does not require Live TV permission, so owned cleanup remains possible after revocation. Returns the serialized session record rather than a bodyless response."
	registerBloemChiDocument[struct{}, bloemJSONOutput[BloemLiveTVSessionResponse]](reg, release)
}
