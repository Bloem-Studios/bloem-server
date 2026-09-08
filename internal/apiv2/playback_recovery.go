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

// PlaybackRecoveryReceipt is the optional additive field on the existing
// mutation object. Its enclosing conditional constrains complete envelopes;
// the STOP operation further restricts recovery to the matching HTTP status.
type PlaybackRecoveryReceipt struct {
	PlaybackRecoveryIdentity
	State    string                    `json:"state" enum:"draining,aborted"`
	Accepted *PlaybackRecoveryAccepted `json:"accepted,omitempty" nullable:"false"`
}

// Huma exposes JSON Schema's if/then keywords through Extensions. These are
// standard OpenAPI 3.1 constraints, not custom x-annotations. They apply only
// to the newly declared recovery property, leaving all ordinary bodies valid.
// Tests evaluate the emitted schemas with a full JSON Schema 2020-12 validator.
func constrainPlaybackRecovery(schema, then *huma.Schema) *huma.Schema {
	schema.Extensions = map[string]any{
		"if":   &huma.Schema{Required: []string{"recovery"}},
		"then": then,
	}
	return schema
}
func (PlaybackDecision) TransformSchema(r huma.Registry, schema *huma.Schema) *huma.Schema {
	return constrainPlaybackRecovery(schema, r.Schema(reflect.TypeFor[PlaybackRecoveryStart](), true, ""))
}
func (PlaybackMutation) TransformSchema(r huma.Registry, schema *huma.Schema) *huma.Schema {
	return constrainPlaybackRecovery(schema, &huma.Schema{OneOf: []*huma.Schema{
		r.Schema(reflect.TypeFor[PlaybackRecoveryPending](), true, ""),
		r.Schema(reflect.TypeFor[PlaybackRecoveryStop](), true, ""),
	}})
}

func playbackRecoveryResponseSchema(r huma.Registry, ordinary, recovery reflect.Type) *huma.Schema {
	// Preserve the original response reference. Its conditional sibling only
	// narrows the new recovery variant for this operation's status code.
	ref := *r.Schema(ordinary, true, "")
	return constrainPlaybackRecovery(&ref, r.Schema(recovery, true, ""))
}
func playbackOrdinaryResponseSchema(r huma.Registry, ordinary reflect.Type) *huma.Schema {
	ref := *r.Schema(ordinary, true, "")
	ref.Not = &huma.Schema{Required: []string{"recovery"}}
	return &ref
}

// The value wrappers only marshal runtime responses. Public source schemas
// retain the existing PlaybackDecision and PlaybackMutation objects/references.
type PlaybackStartResult struct{ value any }

func (v PlaybackStartResult) MarshalJSON() ([]byte, error) { return json.Marshal(v.value) }
func (PlaybackStartResult) Schema(r huma.Registry) *huma.Schema {
	return r.Schema(reflect.TypeFor[PlaybackDecision](), true, "")
}

type PlaybackStopResult struct{ value any }

func (v PlaybackStopResult) MarshalJSON() ([]byte, error) { return json.Marshal(v.value) }
func (PlaybackStopResult) Schema(r huma.Registry) *huma.Schema {
	return r.Schema(reflect.TypeFor[PlaybackMutation](), true, "")
}

type PlaybackStopOutput struct {
	Status int
	Body   PlaybackStopResult
}
