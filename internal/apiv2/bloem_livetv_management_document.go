package apiv2

import (
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/livetv/gracenote"
	"github.com/Silo-Server/silo-server/internal/livetv/schedulesdirect"
)

// Mirrors the actual client JSON, including custom LiveSession serialization.
type BloemLiveTVSessionFields livetv.LiveSession
type BloemLiveTVSessionResponse struct {
	StreamURL string `json:"stream_url"`
	BloemLiveTVSessionFields
}
type BloemLiveTVChannelPatch struct {
	_              struct{} `additionalProperties:"true"`
	Enabled        *bool    `json:"enabled,omitempty"`
	NumberOverride *string  `json:"number_override,omitempty"`
	GuideStationID *string  `json:"guide_station_id,omitempty"`
}
type BloemLiveTVGuideSourcePatch struct {
	_           struct{}           `additionalProperties:"true"`
	Type        *string            `json:"type,omitempty"`
	Priority    *int               `json:"priority,omitempty"`
	Enabled     *bool              `json:"enabled,omitempty"`
	DisplayName *string            `json:"display_name,omitempty"`
	Config      *map[string]string `json:"config,omitempty"`
}
type BloemLiveTVAddTunerRequest struct {
	_              struct{} `additionalProperties:"true"`
	URL            string   `json:"url,omitempty" doc:"Preferred base URL, host or discover.json URL; one address alias is required."`
	DiscoverURL    string   `json:"discover_url,omitempty" doc:"Legacy address alias."`
	DeviceID       string   `json:"device_id,omitempty" doc:"Legacy address alias; not the discovered hardware ID."`
	Type           string   `json:"type,omitempty" doc:"Xtream is rejected here; use the dedicated provider endpoint."`
	Name           string   `json:"name,omitempty"`
	Username       string   `json:"username,omitempty" writeOnly:"true"`
	Password       string   `json:"password,omitempty" writeOnly:"true"`
	MaxConnections int      `json:"max_connections,omitempty"`
}
type BloemLiveTVGuideSourceCreate struct {
	_           struct{}          `additionalProperties:"true"`
	ID          string            `json:"id,omitempty"`
	Type        string            `json:"type"`
	Priority    int               `json:"priority,omitempty"`
	Enabled     bool              `json:"enabled,omitempty"`
	DisplayName string            `json:"display_name,omitempty"`
	Config      map[string]string `json:"config,omitempty"`
	Status      string            `json:"status,omitempty"`
	LastError   string            `json:"last_error,omitempty"`
	LastSyncAt  *time.Time        `json:"last_sync_at,omitempty"`
	NextSyncAt  *time.Time        `json:"next_sync_at,omitempty"`
}
type BloemLiveTVClientCapabilities struct {
	_                      struct{} `additionalProperties:"true"`
	CodecsVideo            []string `json:"codecs_video,omitempty"`
	CodecsAudio            []string `json:"codecs_audio,omitempty"`
	AudioPassthroughCodecs []string `json:"audio_passthrough_codecs,omitempty"`
	MaxAudioChannels       int      `json:"max_audio_channels,omitempty"`
	MaxResolution          string   `json:"max_resolution,omitempty"`
}
type BloemLiveTVSchedulesDirectLineupsRequest struct {
	_            struct{} `additionalProperties:"true"`
	Username     string   `json:"username" writeOnly:"true"`
	Password     string   `json:"password,omitempty" writeOnly:"true"`
	PasswordSHA1 string   `json:"password_sha1,omitempty" writeOnly:"true"`
	Country      string   `json:"country,omitempty"`
	PostalCode   string   `json:"postalcode"`
}
type BloemLiveTVXMLSyncLineupsRequest struct {
	_          struct{} `additionalProperties:"true"`
	Country    string   `json:"country,omitempty"`
	PostalCode string   `json:"postalcode"`
	Lang       string   `json:"lang,omitempty"`
}
type BloemLiveTVScheduleRecordingRequest struct {
	_         struct{}   `additionalProperties:"true"`
	ProgramID string     `json:"program_id,omitempty" doc:"Select a guide program, or supply a manual channel and time window."`
	ChannelID string     `json:"channel_id,omitempty"`
	Start     *time.Time `json:"start,omitempty"`
	Stop      *time.Time `json:"stop,omitempty"`
	Title     string     `json:"title,omitempty"`
}
type BloemLiveTVCreateSeriesRuleRequest struct {
	_          struct{} `additionalProperties:"true"`
	SeriesID   string   `json:"series_id,omitempty"`
	ChannelID  *string  `json:"channel_id,omitempty"`
	TitleMatch string   `json:"title_match,omitempty"`
	NewOnly    bool     `json:"new_only,omitempty"`
	KeepLast   int      `json:"keep_last,omitempty"`
	Enabled    *bool    `json:"enabled,omitempty" doc:"Defaults to true."`
}
type BloemLiveTVDiscoverRequest struct {
	livetv.DiscoverTunersRequest
	_ struct{} `additionalProperties:"true"`
}

type BloemLiveTVTunersResponse struct {
	Tuners []livetv.Tuner `json:"tuners"`
}
type BloemLiveTVGuideSourcesResponse struct {
	GuideSources []livetv.GuideSource `json:"guide_sources"`
}
type BloemLiveTVSchedulesDirectLineup schedulesdirect.LineupOption
type BloemLiveTVXMLSyncLineup gracenote.LineupOption

type BloemLiveTVSchedulesDirectLineupsResponse struct {
	Lineups []BloemLiveTVSchedulesDirectLineup `json:"lineups"`
}
type BloemLiveTVXMLSyncLineupsResponse struct {
	Lineups []BloemLiveTVXMLSyncLineup `json:"lineups"`
}
type BloemLiveTVScanFallback struct {
	ID     string `json:"id"`
	Status string `json:"status" enum:"scanned"`
}
type BloemLiveTVSyncFallback struct {
	ID     string `json:"id"`
	Status string `json:"status" enum:"ready"`
}

// bloemLiveTVGate is the permission gate mountLiveTVRoutes adds after the
// native client group's gates (bloemClientDocumentOp).
type bloemLiveTVGate int

const (
	// bloemLiveTVViewer routes run RequireLiveTVAccess.
	bloemLiveTVViewer bloemLiveTVGate = iota
	// bloemLiveTVUngated routes add no permission gate: capability discovery
	// and owned-session release stay available after a revocation.
	bloemLiveTVUngated
	// bloemLiveTVMedia routes run RequireLiveTVStreamAccess: Live TV and
	// playback permission.
	bloemLiveTVMedia
	// bloemLiveTVAdmin routes run the acting-administrator gate instead.
	bloemLiveTVAdmin
)

const (
	bloemLiveTVForbidden         = " live_tv_forbidden: Live TV permission is not granted."
	bloemLiveTVPlaybackForbidden = " playback_forbidden: playback permission is not granted."
	// bloemLiveTVOwnership describes LiveTVHandler.canManageOtherViewers and
	// the service's ownerMatches check.
	bloemLiveTVOwnership = " Sessions, recordings and series rules belong to the account and profile that created them; another viewer's entry answers 404. An administrator whose selected profile is the account's primary, non-child profile may manage every viewer's entries."
)

func bloemLiveTVDocumentOp(reg *Registry, method, path, id, summary string, status int, gate bloemLiveTVGate) huma.Operation {
	op := bloemClientDocumentOp(reg, method, path, id, "livetv", summary, status)
	bloemDocumentPathParameters(&op)
	op.Description = "Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted."
	forbidden := bloemClientGate403
	failed := "internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld."
	switch gate {
	case bloemLiveTVViewer:
		op.Description += " Requires the freshly resolved Live TV permission."
		forbidden += bloemLiveTVForbidden
	case bloemLiveTVUngated:
		op.Description += " Does not require Live TV permission."
	case bloemLiveTVMedia:
		op.Description += " Requires both current Live TV and playback permission."
		forbidden += bloemLiveTVForbidden + bloemLiveTVPlaybackForbidden
	case bloemLiveTVAdmin:
		op.Description += " Requires acting-administrator authority: the admin role, exercised through the account's primary profile."
		forbidden += " forbidden: the caller lacks the admin role or is not acting through the account's primary profile."
		failed = "internal_error: the viewer's access or acting-administrator authority could not be resolved, or the Live TV request failed; internal details are withheld."
	}
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{403: forbidden, 500: failed})
	return op
}

// bloemLiveTVStorageUnavailable is the 503 writeLiveTVRequestError gives
// livetv.ErrNotConfigured, which in a deployment means no SECRET_KEY cipher.
const bloemLiveTVStorageUnavailable = "dependency_unavailable: encrypted Xtream provider storage is unavailable."

// bloemLiveTVUnavailable extends the client gates' 503 with a handler outcome,
// keeping the Retry-After header the gates document.
func bloemLiveTVUnavailable(op *huma.Operation, outcome string) {
	op.Responses["503"].Description += " " + outcome
}

func bloemLiveTVDocumentUnion[A, B any](reg *Registry, op *huma.Operation) {
	schemas := reg.api.OpenAPI().Components.Schemas
	op.Responses["200"] = &huma.Response{Description: "The refreshed record, or a minimal status record if it disappeared after the operation.", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{AnyOf: []*huma.Schema{schemas.Schema(reflect.TypeFor[A](), true, ""), schemas.Schema(reflect.TypeFor[B](), true, "")}}}}}
}

// Error texts shared by more than one Live TV operation.
const (
	bloemLiveTVInvalidBody       = "invalid_body: the body is not valid JSON."
	bloemLiveTVGuideSourceErrors = "invalid_argument: the type is unsupported; Schedules Direct username, password or password_sha1 (a SHA-1 hex digest) or lineup is missing, or Schedules Direct rejected them; an XML sync postal code or lineup is missing; or an Xtream guide source names no configured provider or one that already has a guide source."
)

func registerBloemLiveTVManagementDocument(reg *Registry) {
	// The tuner store returns nil for an empty codec list, which serializes as
	// null. Huma defaults array schemas to nonnull; correct this native registry
	// without changing the runtime wire type or the separate v2 document.
	reg.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[livetv.Tuner](), false, "").Properties["transcode_codecs"].Nullable = true

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/programs/{programId}", "getBloemLiveTVProgram", "Read a guide program.", 200, bloemLiveTVViewer)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			404: "not_found: the program does not exist, or X-Profile-Id names no profile of this account.",
		})
		registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.Program]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/recordings", "listBloemLiveTVRecordings", "List recordings visible to this viewer.", 200, bloemLiveTVViewer)
		op.Description += bloemLiveTVOwnership
		bloemDocumentQuery(&op, "status", "Optional recording status filter.", "string")
		registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.RecordingsResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/recordings", "scheduleBloemLiveTVRecording", "Schedule a guide-program or manual recording.", 201, bloemLiveTVViewer)
		op.Description += " User and profile ownership come from authentication, never from the body. A program_id takes its channel and time window from the guide. Tuner capacity is claimed when the recording starts, not when it is scheduled. Creation is not replay-safe."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. " + bloemLiveTVInvalidBody + " invalid_argument: channel_id, start and stop are required without a program_id.",
			404: "not_found: program_id names no guide program, or X-Profile-Id names no profile of this account.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVScheduleRecordingRequest], bloemJSONOutput[livetv.Recording]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "DELETE", "/livetv/recordings/{recordingId}", "cancelBloemLiveTVRecording", "Cancel an owned recording and return its updated record.", 200, bloemLiveTVViewer)
		op.Description += bloemLiveTVOwnership
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			404: "not_found: the recording does not exist or belongs to another viewer, or X-Profile-Id names no profile of this account.",
		})
		registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.Recording]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/series-rules", "listBloemLiveTVSeriesRules", "List viewer-owned DVR series rules.", 200, bloemLiveTVViewer)
		op.Description += bloemLiveTVOwnership
		registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.SeriesRulesResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/series-rules", "createBloemLiveTVSeriesRule", "Create a viewer-owned DVR series rule.", 201, bloemLiveTVViewer)
		op.Description += " Series ID, channel ID or title match is required. User/profile ownership comes from authentication. Creation is not replay-safe."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. " + bloemLiveTVInvalidBody + " invalid_argument: series_id, channel_id or a non-blank title_match is required.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVCreateSeriesRuleRequest], bloemJSONOutput[livetv.SeriesRule]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "DELETE", "/livetv/series-rules/{ruleId}", "deleteBloemLiveTVSeriesRule", "Delete an owned DVR series rule.", 204, bloemLiveTVViewer)
		op.Description += bloemLiveTVOwnership + " For such an administrator, deleting an absent rule also answers 204."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			404: "not_found: the rule does not exist or belongs to another viewer, or X-Profile-Id names no profile of this account.",
		})
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/sessions/{sessionId}/heartbeat", "heartbeatBloemLiveTVSession", "Renew an owned active tuner session.", 204, bloemLiveTVViewer)
		op.Description += bloemLiveTVOwnership
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			404: "not_found: the session does not exist, is no longer active or belongs to another viewer, or X-Profile-Id names no profile of this account.",
		})
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	// GET and HEAD share one handler: HEAD stops after the session and source
	// checks, so it never opens the upstream and never answers 409 or 502.
	streamErrors := map[int]string{
		400: "bad_request: X-Profile-Id is missing. invalid_argument: the channel's stored source is not an allowed media address.",
		404: "not_found: the session does not exist, is no longer active or belongs to another viewer, or its channel or source is gone; or X-Profile-Id names no profile of this account.",
	}
	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/sessions/{sessionId}/stream", "streamBloemLiveTVSession", "Stream MPEG-TS media.", 200, bloemLiveTVMedia)
		op.Description += bloemLiveTVOwnership + " Requires an active session. Delivery tickets are not accepted on this route. GET proxies the MPEG-TS stream and renews the tuner lease until disconnect. HEAD checks the session and its source and returns media headers without fetching stream bytes."
		bloemDocumentErrors[BloemNativeError](reg, &op, streamErrors)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			409: "limit_exceeded: the Xtream provider's connection budget is in use.",
			502: "upstream_error: the tuner stream could not be opened or answered a non-success status.",
		})
		op.Responses["200"] = &huma.Response{Description: "MPEG-TS media stream.", Headers: map[string]*huma.Header{"Content-Type": {Schema: &huma.Schema{Type: "string", Enum: []any{"video/mp2t"}}}}}
		op.Responses["200"].Content = map[string]*huma.MediaType{"video/mp2t": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}
		bloemLiveTVUnavailable(&op, bloemLiveTVStorageUnavailable)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "HEAD", "/livetv/sessions/{sessionId}/stream", "probeBloemLiveTVSession", "Check MPEG-TS stream headers.", 200, bloemLiveTVMedia)
		op.Description += bloemLiveTVOwnership + " Requires an active session. Delivery tickets are not accepted on this route. HEAD checks the session and its source and returns media headers without fetching stream bytes or opening the upstream. GET proxies the MPEG-TS stream and renews the tuner lease until disconnect."
		bloemDocumentErrors[BloemNativeError](reg, &op, streamErrors)
		op.Responses["200"] = &huma.Response{Description: "MPEG-TS media headers; no body.", Headers: map[string]*huma.Header{"Content-Type": {Schema: &huma.Schema{Type: "string", Enum: []any{"video/mp2t"}}}}}
		bloemLiveTVUnavailable(&op, bloemLiveTVStorageUnavailable)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/live-hls/{playbackId}/{name}", "getBloemLiveTVHLSMedia", "Fetch a session-bound HLS playlist or MPEG-TS segment.", 200, bloemLiveTVMedia)
		op.Security = []map[string][]string{{"bloemLiveTVDeliveryTicket": {}}}
		// HLS resolves its profile from the ticket, without caller profile headers.
		kept := op.Parameters[:0]
		for _, parameter := range op.Parameters {
			if parameter.In != "header" {
				kept = append(kept, parameter)
			}
			if parameter.Name == "name" {
				parameter.Schema.Pattern = `^(index\.m3u8|seg_[^/\\]*\.ts)$`
			}
		}
		op.Parameters = kept
		op.Description = "Requires the signed st query delivery ticket from the session-start URL; an account bearer alone is insufficient, and no account, tenant or profile header is read. The ticket must have purpose=bloem_livetv_hls, match this playback ID, and carry user/profile identity. Current viewer policy and ownership are resolved on every fetch: both Live TV and playback permission remain required, and only the ticket's own viewer may fetch. name is index.m3u8 or seg_*.ts, without additional path separators or escaped path components. Playlist relative segment URLs retain only the delivery ticket. Playlists use Cache-Control: no-store; segments support standard file range and conditional requests. A session owned by another API process is proxied to it."
		op.Responses = map[string]*huma.Response{}
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			401: "Plain text: the delivery path is invalid, or the st ticket is missing, invalid, expired, for another playback ID or purpose, or names no resolvable viewer. unauthorized (JSON): the ticket viewer's access could not be resolved.",
			403: "live_tv_forbidden: Live TV permission is not granted. playback_forbidden: playback permission is not granted.",
			404: "not_found (JSON): the playback session does not exist, is no longer active, belongs to another viewer, or has no such playlist or segment. Plain text: the segment disappeared while being served.",
			500: "internal_error: the request failed; internal details are withheld.",
			503: "dependency_unavailable: the API process that owns this session is temporarily unreachable; retry after Retry-After.",
		})
		for _, status := range []string{"401", "404"} {
			op.Responses[status].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
		}
		op.Responses["503"].Headers = map[string]*huma.Header{"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds before retrying."}}
		bloemDocumentRateLimit(reg, &op)
		op.Responses["200"] = &huma.Response{Description: "Playlist text or MPEG-TS segment bytes.", Content: map[string]*huma.MediaType{
			"application/vnd.apple.mpegurl": {Schema: &huma.Schema{Type: "string"}},
			"video/mp2t":                    {Schema: &huma.Schema{Type: "string", Format: "binary"}},
		}}
		op.Responses["206"] = &huma.Response{Description: "Partial MPEG-TS segment.", Content: map[string]*huma.MediaType{"video/mp2t": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}
		op.Responses["304"] = &huma.Response{Description: "Segment has not changed; no body."}
		op.Responses["412"] = &huma.Response{Description: "A segment If-Match or If-Unmodified-Since precondition failed; no body."}
		op.Responses["416"] = &huma.Response{Description: "The segment range is not satisfiable.", Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}}}
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/tuners", "listBloemLiveTVTuners", "List configured tuner metadata.", 200, bloemLiveTVAdmin)
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemLiveTVTunersResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/tuners", "addBloemLiveTVTuner", "Add an HDHomeRun-compatible tuner.", 201, bloemLiveTVAdmin)
		op.Description += " JSON body limit 32 KiB. Xtream providers use /tuners/xtream. The device is discovered at the address and its lineup scanned before the response; if the device answers discovery but its lineup can't be read, the tuner is saved with the failure in last_error and the request answers 201 with status error; scan it again later. Adding a device that is already saved updates that tuner."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON or exceeds 32 KiB. invalid_argument: type is xtream or another non-HDHomeRun type, provider credentials or max_connections were supplied, the address is missing, invalid or not an allowed media address, or the device reported no device ID.",
			500: "internal_error: the viewer's access or acting-administrator authority could not be resolved, the device could not be discovered, or saving the tuner or its channels failed; internal details are withheld.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVAddTunerRequest], bloemJSONOutput[livetv.Tuner]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/tuners/discover", "discoverBloemLiveTVTuners", "Discover local HDHomeRun-compatible tuner candidates.", 200, bloemLiveTVAdmin)
		op.Description += " The JSON body is optional. timeout_ms defaults to 2000 and is bounded to 10000; probe_urls selects HTTP discovery candidates. Probe failures are reported in notes rather than as errors. Discovery does not save a tuner."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. invalid_body: the optional body is not valid JSON.",
		})
		registerBloemChiDocument[bloemOptionalJSONInput[BloemLiveTVDiscoverRequest], bloemJSONOutput[livetv.DiscoverTunersResult]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "DELETE", "/livetv/tuners/{tunerId}", "deleteBloemLiveTVTuner", "Delete a tuner.", 204, bloemLiveTVAdmin)
		op.Description += " Deleting an absent tuner also answers 204. Deleting an Xtream provider also deletes its guide source, and is refused while it has active streams or recordings."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			409: "limit_exceeded: the Xtream provider still has active streams or recordings.",
			500: "internal_error: the viewer's access or acting-administrator authority could not be resolved, or the deletion failed; internal details are withheld.",
		})
		bloemLiveTVUnavailable(&op, bloemLiveTVStorageUnavailable)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/tuners/{tunerId}/scan", "scanBloemLiveTVTuner", "Scan a saved tuner and refresh its channel lineup.", 200, bloemLiveTVAdmin)
		bloemLiveTVDocumentUnion[livetv.Tuner, BloemLiveTVScanFallback](reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. invalid_argument: the tuner's stored address or Xtream provider configuration is not allowed.",
			404: "not_found: the tuner or its stored Xtream credentials do not exist, or X-Profile-Id names no profile of this account.",
			500: "internal_error: the viewer's access or acting-administrator authority could not be resolved, or the device or provider could not be scanned; internal details are withheld.",
		})
		bloemLiveTVUnavailable(&op, bloemLiveTVStorageUnavailable)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "PATCH", "/livetv/channels/{channelId}", "patchBloemLiveTVChannel", "Change channel enablement, number override or guide mapping.", 200, bloemLiveTVAdmin)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. " + bloemLiveTVInvalidBody,
			404: "not_found: the channel does not exist, or X-Profile-Id names no profile of this account.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVChannelPatch], bloemJSONOutput[livetv.ChannelResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/guide-sources", "listBloemLiveTVGuideSources", "List configured guide sources.", 200, bloemLiveTVAdmin)
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemLiveTVGuideSourcesResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/guide-sources", "createBloemLiveTVGuideSource", "Create a guide source.", 201, bloemLiveTVAdmin)
		op.Description += " At most three guide sources may be enabled. New Schedules Direct credentials or lineups are verified with Schedules Direct before saving."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. " + bloemLiveTVInvalidBody + " " + bloemLiveTVGuideSourceErrors,
			404: "not_found: the Xtream provider was removed while the source was saved, or X-Profile-Id names no profile of this account.",
			409: "limit_exceeded: three guide sources are already enabled.",
			500: "internal_error: the viewer's access or acting-administrator authority could not be resolved, Schedules Direct could not be reached, or saving failed; internal details are withheld.",
		})
		bloemLiveTVUnavailable(&op, bloemLiveTVStorageUnavailable)
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVGuideSourceCreate], bloemJSONOutput[livetv.GuideSource]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "PATCH", "/livetv/guide-sources/{sourceId}", "updateBloemLiveTVGuideSource", "Merge supplied guide-source fields with current configuration.", 200, bloemLiveTVAdmin)
		op.Description += " At most three guide sources may be enabled. Changed Schedules Direct credentials or lineups are verified with Schedules Direct before saving."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. " + bloemLiveTVInvalidBody + " " + bloemLiveTVGuideSourceErrors,
			404: "not_found: the guide source does not exist, or its Xtream provider was removed while it was saved; or X-Profile-Id names no profile of this account.",
			409: "limit_exceeded: three other guide sources are already enabled.",
			500: "internal_error: the viewer's access or acting-administrator authority could not be resolved, Schedules Direct could not be reached, or saving failed; internal details are withheld.",
		})
		bloemLiveTVUnavailable(&op, bloemLiveTVStorageUnavailable)
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVGuideSourcePatch], bloemJSONOutput[livetv.GuideSource]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "DELETE", "/livetv/guide-sources/{sourceId}", "deleteBloemLiveTVGuideSource", "Delete a guide source.", 204, bloemLiveTVAdmin)
		op.Description += " Deleting an absent guide source also answers 204."
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/guide-sources/{sourceId}/sync", "syncBloemLiveTVGuideSource", "Synchronize a saved guide source.", 200, bloemLiveTVAdmin)
		op.Description += " Runs the synchronization before answering. An Xtream guide synchronization already running for the source answers 409."
		bloemLiveTVDocumentUnion[livetv.GuideSource, BloemLiveTVSyncFallback](reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. invalid_argument: the source type is unsupported, an Xtream guide source is disabled or its provider configuration is not allowed, required configuration is missing, no channel maps to a guide station, or the guide provider rejected the stored configuration.",
			404: "not_found: the guide source or its Xtream provider does not exist, or X-Profile-Id names no profile of this account.",
			409: "conflict: an Xtream synchronization of this source is already running, or the source changed as it started.",
			500: "internal_error: the viewer's access or acting-administrator authority could not be resolved, the guide provider could not be reached, or saving failed; internal details are withheld.",
		})
		bloemLiveTVUnavailable(&op, bloemLiveTVStorageUnavailable)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/guide-sources/schedules-direct/lineups", "lookupBloemLiveTVSchedulesDirectLineups", "Look up Schedules Direct lineups by postal code.", 200, bloemLiveTVAdmin)
		op.Description += " Supply password or password_sha1; credentials are used for the lookup and not returned. country defaults to the Schedules Direct default."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. " + bloemLiveTVInvalidBody + " invalid_argument: username, a password or password_sha1, and postalcode are required, or Schedules Direct rejected the request.",
			500: "internal_error: the viewer's access or acting-administrator authority could not be resolved, or Schedules Direct could not be reached; internal details are withheld.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVSchedulesDirectLineupsRequest], bloemJSONOutput[BloemLiveTVSchedulesDirectLineupsResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/guide-sources/xml-sync/lineups", "lookupBloemLiveTVXMLSyncLineups", "Look up Gracenote providers by postal code.", 200, bloemLiveTVAdmin)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing. " + bloemLiveTVInvalidBody + " invalid_argument: postalcode is required.",
			500: "internal_error: the viewer's access or acting-administrator authority could not be resolved, or the provider lookup failed; internal details are withheld.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVXMLSyncLineupsRequest], bloemJSONOutput[BloemLiveTVXMLSyncLineupsResponse]](reg, op)
	}
}
