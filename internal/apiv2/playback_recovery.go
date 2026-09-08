package apiv2

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/danielgtaylor/huma/v2"
)

// Recovery is optional on services that do not implement retained initial flows.
// Both hooks run after route validation and before ordinary live-owner lookup.
type playbackRecoveryService interface {
	RecoverInitialPlaybackStart(context.Context, handlers.PlaybackCaller, playback.StartRequestV3) (int, any, bool, error)
	ResolvePlaybackOwnerLoss(context.Context, handlers.PlaybackCaller, playback.InitialRecoveryLookupV3) (int, any, bool, error)
}

type PlaybackRecoveryIdentity struct {
	RecoveryID        string `json:"recovery_id" format:"uuid"`
	PlaybackAttemptID string `json:"playback_attempt_id"`
	SessionID         string `json:"session_id" format:"uuid"`
	Reason            string `json:"reason" enum:"owner_lost"`
}

type PlaybackRecoveryDrainingReceipt struct {
	PlaybackRecoveryIdentity
	State string `json:"state" enum:"draining"`
}
type PlaybackRecoveryAbortedReceipt struct {
	PlaybackRecoveryIdentity
	State    string                    `json:"state" enum:"aborted"`
	Accepted *PlaybackRecoveryAccepted `json:"accepted,omitempty" nullable:"false"`
}
type PlaybackRecoveryAccepted struct {
	TimelineID   string   `json:"timeline_id,omitempty" minLength:"64" maxLength:"64" pattern:"^[0-9a-f]{64}$"`
	ItemPosition *float64 `json:"item_position,omitempty" minimum:"0" nullable:"false"`
	Sequence     int64    `json:"sequence" minimum:"1"`
	Position     float64  `json:"position" minimum:"0"`
	IsPaused     bool     `json:"is_paused"`
}
type PlaybackRecoveryPending struct {
	Outcome  string                          `json:"outcome" enum:"draining"`
	Recovery PlaybackRecoveryDrainingReceipt `json:"recovery"`
}
type PlaybackRecoveryStart struct {
	ProtocolVersion int                            `json:"protocol_version" enum:"3"`
	ServerFeatures  []string                       `json:"server_features"`
	Outcome         string                         `json:"outcome" enum:"adaptation_unavailable"`
	Terminal        PlaybackRecoveryTerminal       `json:"terminal"`
	Recovery        PlaybackRecoveryAbortedReceipt `json:"recovery"`
}
type PlaybackRecoveryTerminal struct {
	Reason    string `json:"reason" enum:"playback_owner_lost"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable" enum:"false"`
}
type PlaybackRecoveryStop struct {
	Outcome  string                         `json:"outcome" enum:"aborted"`
	Recovery PlaybackRecoveryAbortedReceipt `json:"recovery"`
}

// The response union preserves the ordinary decision representation, while a
// draining START has no protocol/terminal/playable fields at all.
type PlaybackStartResult struct{ value any }

func (v PlaybackStartResult) MarshalJSON() ([]byte, error) { return json.Marshal(v.value) }
func (PlaybackStartResult) Schema(r huma.Registry) *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{
		r.Schema(reflect.TypeFor[PlaybackDecision](), true, ""),
		r.Schema(reflect.TypeFor[PlaybackRecoveryStart](), true, ""),
		r.Schema(reflect.TypeFor[PlaybackRecoveryPending](), true, ""),
	}}
}

type PlaybackStopResult struct{ value any }

func (v PlaybackStopResult) MarshalJSON() ([]byte, error) { return json.Marshal(v.value) }
func (PlaybackStopResult) Schema(r huma.Registry) *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{
		r.Schema(reflect.TypeFor[PlaybackMutation](), true, ""),
		r.Schema(reflect.TypeFor[PlaybackRecoveryStop](), true, ""),
		r.Schema(reflect.TypeFor[PlaybackRecoveryPending](), true, ""),
	}}
}

type PlaybackStopOutput struct {
	Status int
	Body   PlaybackStopResult
}
