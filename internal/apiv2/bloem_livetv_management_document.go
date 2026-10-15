package apiv2

import (
	"reflect"
	"time"

	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/livetv/gracenote"
	"github.com/Silo-Server/silo-server/internal/livetv/schedulesdirect"
	"github.com/danielgtaylor/huma/v2"
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

func bloemLiveTVDocumentOp(reg *Registry, method, path, id, summary string, status int, admin bool) huma.Operation {
	op := bloemChiDocumentOp(reg, method, path, id, "livetv", summary, "bearerAuth", status)
	bloemDocumentPathParameters(&op)
	op.Parameters = append(op.Parameters,
		&huma.Param{Name: "X-Profile-Id", In: "header", Required: true, Description: "Selected account profile. Direct-profile sessions are not admitted to native Live TV routes.", Schema: &huma.Schema{Type: "string"}},
		&huma.Param{Name: "X-Profile-Token", In: "header", Description: "Verification proof when the selected profile is PIN-locked.", Schema: &huma.Schema{Type: "string"}},
	)
	op.Description = "Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority."
	if admin {
		op.Description += " Requires acting-administrator authority and the account's primary non-child profile."
	} else {
		op.Description += " Requires the freshly resolved Live TV permission, except that capability discovery and owned-session release remain available after permission revocation. Session and DVR ownership is enforced; an acting administrator on a verified primary profile may manage other viewers."
	}
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		400: "invalid_body or invalid_argument: request JSON, parameters or values are invalid.",
		401: "Account/profile authentication or required profile verification is missing or expired.",
		403: "Live TV, playback, acting-admin or selected-profile authority is missing.",
		404: "not_found: the resource is absent or not owned by this viewer.",
		409: "no_tuner or limit_exceeded: tuner or recording capacity is unavailable.",
		500: "internal_error: request failed; internal dependency details are withheld.",
		501: "not_implemented: the requested Live TV operation is unsupported.",
		503: "dependency_unavailable: an owner or authority dependency is unavailable.",
	})
	return op
}

func bloemLiveTVDocumentUnion[A, B any](reg *Registry, op *huma.Operation) {
	schemas := reg.api.OpenAPI().Components.Schemas
	op.Responses["200"] = &huma.Response{Description: "The refreshed record, or a minimal status record if it disappeared after the operation.", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{AnyOf: []*huma.Schema{schemas.Schema(reflect.TypeFor[A](), true, ""), schemas.Schema(reflect.TypeFor[B](), true, "")}}}}}
}

func registerBloemLiveTVManagementDocument(reg *Registry) {
	// The tuner store returns nil for an empty codec list, which serializes as
	// null. Huma defaults array schemas to nonnull; correct this native registry
	// without changing the runtime wire type or the separate v2 document.
	reg.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[livetv.Tuner](), false, "").Properties["transcode_codecs"].Nullable = true

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/programs/{programId}", "getBloemLiveTVProgram", "Read a guide program.", 200, false)
		registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.Program]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/recordings", "listBloemLiveTVRecordings", "List recordings visible to this viewer.", 200, false)
		bloemDocumentQuery(&op, "status", "Optional recording status filter.", "string")
		registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.RecordingsResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/recordings", "scheduleBloemLiveTVRecording", "Schedule a guide-program or manual recording.", 201, false)
		op.Description += " User and profile ownership come from authentication, never from the body. Creation is not replay-safe."
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVScheduleRecordingRequest], bloemJSONOutput[livetv.Recording]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "DELETE", "/livetv/recordings/{recordingId}", "cancelBloemLiveTVRecording", "Cancel an owned recording and return its updated record.", 200, false)
		registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.Recording]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/series-rules", "listBloemLiveTVSeriesRules", "List viewer-owned DVR series rules.", 200, false)
		registerBloemChiDocument[struct{}, bloemJSONOutput[livetv.SeriesRulesResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/series-rules", "createBloemLiveTVSeriesRule", "Create a viewer-owned DVR series rule.", 201, false)
		op.Description += " Series ID, channel ID or title match is required. User/profile ownership comes from authentication. Creation is not replay-safe."
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVCreateSeriesRuleRequest], bloemJSONOutput[livetv.SeriesRule]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "DELETE", "/livetv/series-rules/{ruleId}", "deleteBloemLiveTVSeriesRule", "Delete an owned DVR series rule.", 204, false)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/sessions/{sessionId}/heartbeat", "heartbeatBloemLiveTVSession", "Renew an owned active tuner session.", 204, false)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/sessions/{sessionId}/stream", "streamBloemLiveTVSession", "Stream MPEG-TS media.", 200, false)
		op.Description += " Requires both current Live TV and playback permission and an active session owned by this viewer. Delivery tickets are not accepted on this route. HEAD checks the session/upstream locator and returns media headers without fetching stream bytes. GET proxies the MPEG-TS stream and renews the tuner lease until disconnect."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{502: "upstream_error: upstream media could not be opened."})
		op.Responses["200"] = &huma.Response{Description: "MPEG-TS media stream.", Headers: map[string]*huma.Header{"Content-Type": {Schema: &huma.Schema{Type: "string", Enum: []any{"video/mp2t"}}}}}
		op.Responses["200"].Content = map[string]*huma.MediaType{"video/mp2t": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "HEAD", "/livetv/sessions/{sessionId}/stream", "probeBloemLiveTVSession", "Check MPEG-TS stream headers.", 200, false)
		op.Description += " Requires both current Live TV and playback permission and an active session owned by this viewer. Delivery tickets are not accepted on this route. HEAD checks the session/upstream locator and returns media headers without fetching stream bytes. GET proxies the MPEG-TS stream and renews the tuner lease until disconnect."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{502: "upstream_error: upstream media could not be opened."})
		op.Responses["200"] = &huma.Response{Description: "MPEG-TS media stream.", Headers: map[string]*huma.Header{"Content-Type": {Schema: &huma.Schema{Type: "string", Enum: []any{"video/mp2t"}}}}}
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/live-hls/{playbackId}/{name}", "getBloemLiveTVHLSMedia", "Fetch a session-bound HLS playlist or MPEG-TS segment.", 200, false)
		op.Security = []map[string][]string{{"bloemLiveTVDeliveryTicket": {}}}
		// HLS resolves its profile from the ticket, without caller profile headers.
		kept := op.Parameters[:0]
		for _, parameter := range op.Parameters {
			if parameter.In != "header" {
				kept = append(kept, parameter)
			}
		}
		op.Parameters = kept
		op.Description = "Requires the signed st query delivery ticket from the session-start URL; an account bearer alone is insufficient. The ticket must have purpose=bloem_livetv_hls, match this playback ID, and carry user/profile identity. Current viewer policy and ownership are resolved on every fetch: both Live TV and playback permission remain required. name is index.m3u8 or seg_*.ts, without additional path separators or escaped path components. Playlist relative segment URLs retain only the delivery ticket. Playlists use Cache-Control: no-store; segments support standard file range and conditional requests."
		op.Responses["200"] = &huma.Response{Description: "Playlist text or MPEG-TS segment bytes.", Content: map[string]*huma.MediaType{
			"application/vnd.apple.mpegurl": {Schema: &huma.Schema{Type: "string"}},
			"video/mp2t":                    {Schema: &huma.Schema{Type: "string", Format: "binary"}},
		}}
		op.Responses["206"] = &huma.Response{Description: "Partial MPEG-TS segment.", Content: map[string]*huma.MediaType{"video/mp2t": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}
		op.Responses["304"] = &huma.Response{Description: "Segment has not changed; no body."}
		for _, status := range []string{"401", "404", "416"} {
			if op.Responses[status] == nil {
				op.Responses[status] = &huma.Response{Description: "Rejected delivery ticket, missing segment or unsatisfiable file range.", Content: map[string]*huma.MediaType{}}
			}
			op.Responses[status].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
		}
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/tuners", "listBloemLiveTVTuners", "List configured tuner metadata.", 200, true)
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemLiveTVTunersResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/tuners", "addBloemLiveTVTuner", "Add an HDHomeRun-compatible tuner.", 201, true)
		op.Description += " JSON body limit 32 KiB. Xtream providers use /tuners/xtream. Creation is not replay-safe."
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVAddTunerRequest], bloemJSONOutput[livetv.Tuner]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/tuners/discover", "discoverBloemLiveTVTuners", "Discover local HDHomeRun-compatible tuner candidates.", 200, true)
		op.Description += " The JSON body is optional. timeout_ms defaults to 2000 and is bounded to 10000; probe_urls selects HTTP discovery candidates. Discovery does not save a tuner."
		registerBloemChiDocument[bloemOptionalJSONInput[BloemLiveTVDiscoverRequest], bloemJSONOutput[livetv.DiscoverTunersResult]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "DELETE", "/livetv/tuners/{tunerId}", "deleteBloemLiveTVTuner", "Delete a tuner.", 204, true)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/tuners/{tunerId}/scan", "scanBloemLiveTVTuner", "Scan a saved tuner and refresh its channel lineup.", 200, true)
		bloemLiveTVDocumentUnion[livetv.Tuner, BloemLiveTVScanFallback](reg, &op)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "PATCH", "/livetv/channels/{channelId}", "patchBloemLiveTVChannel", "Change channel enablement, number override or guide mapping.", 200, true)
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVChannelPatch], bloemJSONOutput[livetv.ChannelResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "GET", "/livetv/guide-sources", "listBloemLiveTVGuideSources", "List configured guide sources.", 200, true)
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemLiveTVGuideSourcesResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/guide-sources", "createBloemLiveTVGuideSource", "Create a guide source.", 201, true)
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVGuideSourceCreate], bloemJSONOutput[livetv.GuideSource]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "PATCH", "/livetv/guide-sources/{sourceId}", "updateBloemLiveTVGuideSource", "Merge supplied guide-source fields with current configuration.", 200, true)
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVGuideSourcePatch], bloemJSONOutput[livetv.GuideSource]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "DELETE", "/livetv/guide-sources/{sourceId}", "deleteBloemLiveTVGuideSource", "Delete a guide source.", 204, true)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/guide-sources/{sourceId}/sync", "syncBloemLiveTVGuideSource", "Synchronize a saved guide source.", 200, true)
		bloemLiveTVDocumentUnion[livetv.GuideSource, BloemLiveTVSyncFallback](reg, &op)
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/guide-sources/schedules-direct/lineups", "lookupBloemLiveTVSchedulesDirectLineups", "Look up Schedules Direct lineups by postal code.", 200, true)
		op.Description += " Supply password or password_sha1; credentials are used for the lookup and not returned."
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVSchedulesDirectLineupsRequest], bloemJSONOutput[BloemLiveTVSchedulesDirectLineupsResponse]](reg, op)
	}

	{
		op := bloemLiveTVDocumentOp(reg, "POST", "/livetv/guide-sources/xml-sync/lineups", "lookupBloemLiveTVXMLSyncLineups", "Look up Gracenote providers by postal code.", 200, true)
		registerBloemChiDocument[bloemJSONInput[BloemLiveTVXMLSyncLineupsRequest], bloemJSONOutput[BloemLiveTVXMLSyncLineupsResponse]](reg, op)
	}
}
