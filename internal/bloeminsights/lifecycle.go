package bloeminsights

import (
	"encoding/json"
	"time"
)

// LifecycleEvent is a playback lifecycle event as the events hub carries it
// on the sessions channel (apiv2.AdminPlaybackLifecycleEvent's wire shape),
// with only the fields stream insights reads. It is decoded here rather than
// imported, since apiv2 depends on the handlers that use this package.
type LifecycleEvent struct {
	SessionID string            `json:"session_id"`
	Seq       int64             `json:"seq"`
	At        time.Time         `json:"at"`
	State     string            `json:"state"`
	EndReason string            `json:"end_reason,omitempty"`
	Session   *LifecycleSession `json:"session,omitempty"`
	History   *LifecycleHistory `json:"history,omitempty"`
}

// LifecycleSession is the live admin row a started, paused or resumed event
// carries.
type LifecycleSession struct {
	UserID                   string    `json:"user_id"`
	ProfileID                string    `json:"profile_id"`
	StartedAt                time.Time `json:"started_at"`
	ClientIP                 string    `json:"client_ip,omitempty"`
	ClientName               string    `json:"client_name,omitempty"`
	ClientVersion            string    `json:"client_version,omitempty"`
	DevicePlatform           string    `json:"device_platform,omitempty"`
	PlayMethod               string    `json:"play_method"`
	EffectivePlayMethod      string    `json:"effective_play_method,omitempty"`
	VideoDecision            string    `json:"video_decision,omitempty"`
	AudioDecision            string    `json:"audio_decision,omitempty"`
	AdaptationReasons        []string  `json:"adaptation_reasons,omitempty"`
	NodeDisplayName          string    `json:"node_display_name,omitempty"`
	RoutingExecutionNodeID   *string   `json:"routing_execution_node_id,omitempty"`
	RoutingExecutionNodeName string    `json:"routing_execution_node_name,omitempty"`
	RoutingEgressNodeID      *string   `json:"routing_egress_node_id,omitempty"`
	RoutingEgressNodeName    string    `json:"routing_egress_node_name,omitempty"`
}

// LifecycleHistory is the history entry a stopped event carries.
type LifecycleHistory struct {
	UserID         string    `json:"user_id"`
	ProfileID      string    `json:"profile_id"`
	PlayMethod     string    `json:"play_method"`
	StartedAt      time.Time `json:"started_at"`
	EndedAt        time.Time `json:"ended_at"`
	ClientIP       *string   `json:"client_ip"`
	ClientName     *string   `json:"client_name"`
	ClientVersion  *string   `json:"client_version"`
	DevicePlatform *string   `json:"device_platform"`
	VideoDecision  *string   `json:"video_decision"`
	AudioDecision  *string   `json:"audio_decision"`
}

// DecodeLifecycleEvent decodes an event's data.
func DecodeLifecycleEvent(raw []byte) (LifecycleEvent, error) {
	var ev LifecycleEvent
	err := json.Unmarshal(raw, &ev)
	return ev, err
}

// ClientIP is the viewer address the event names, or "".
func (ev LifecycleEvent) ClientIP() string {
	if ev.Session != nil {
		return ev.Session.ClientIP
	}
	if ev.History != nil && ev.History.ClientIP != nil {
		return *ev.History.ClientIP
	}
	return ""
}
