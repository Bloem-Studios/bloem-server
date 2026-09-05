package livetv

import "time"

// ChannelsResponse is the existing Live TV JSON response envelope.
type ChannelsResponse struct {
	Channels []ChannelResponse `json:"channels"`
}

// GuideResponse is the existing Live TV JSON response envelope.
type GuideResponse struct {
	Programs []Program `json:"programs"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
}

// SessionStartResponse is the existing Live TV JSON response envelope.
type SessionStartResponse struct {
	SessionID      string `json:"session_id"`
	PlaybackTicket string `json:"playback_ticket"`
	HLSURL         string `json:"hls_url"`
	StreamURL      string `json:"stream_url"`
	Transport      string `json:"transport,omitempty"`
	Note           string `json:"note,omitempty"`
}

// RecordingsResponse is the existing Live TV JSON response envelope.
type RecordingsResponse struct {
	Recordings []Recording `json:"recordings"`
}

// SeriesRulesResponse is the existing Live TV JSON response envelope.
type SeriesRulesResponse struct {
	SeriesRules []SeriesRule `json:"series_rules"`
}

// ScheduleRecordingRequest preserves the existing scheduling request contract.
type ScheduleRecordingRequest struct {
	ProgramID string    `json:"program_id"`
	ChannelID string    `json:"channel_id"`
	Start     time.Time `json:"start"`
	Stop      time.Time `json:"stop"`
	Title     string    `json:"title"`
}

// CreateSeriesRuleRequest preserves the existing scheduling request contract.
type CreateSeriesRuleRequest struct {
	SeriesID   string  `json:"series_id"`
	ChannelID  *string `json:"channel_id"`
	TitleMatch string  `json:"title_match"`
	NewOnly    bool    `json:"new_only"`
	KeepLast   int     `json:"keep_last"`
	Enabled    *bool   `json:"enabled"`
}
