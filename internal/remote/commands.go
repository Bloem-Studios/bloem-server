// Package remote implements the sender side of admin remote control
// (docs/specs/admin-remote-control.md): server-validated command payloads,
// the per-device capability handshake, the remote_commands audit store, and
// the session-scoped delivery service that rides the existing playback
// control socket.
package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// Scope names the delivery rail a command targets.
type Scope string

const (
	ScopeSession Scope = "session"
	ScopeDevice  Scope = "device"
)

// IssuerKind distinguishes the two sender surfaces.
type IssuerKind string

const (
	IssuerAdmin     IssuerKind = "admin"
	IssuerHousehold IssuerKind = "household"
)

// State is the lifecycle of one remote command.
type State string

const (
	StateQueued              State = "queued"
	StateSent                State = "sent"
	StateAccepted            State = "accepted"
	StateRejected            State = "rejected"
	StateRejectedUnsupported State = "rejected_unsupported"
	StateDone                State = "done"
	StateFailed              State = "failed"
	StateExpired             State = "expired"
)

// Terminal reports whether no further transition can follow.
func (s State) Terminal() bool {
	switch s {
	case StateRejected, StateRejectedUnsupported, StateDone, StateFailed, StateExpired:
		return true
	}
	return false
}

// CommandReplan is the S-5a "fix this playback" command. It is a remote
// control name only: the client never sees it, it sees the resulting
// plan_invalidated command exactly as today.
const CommandReplan playback.CommandName = "replan"

// CapabilityVersion is the remote_control handshake version this server
// speaks.
const CapabilityVersion = 1

var (
	ErrUnknownCommand      = errors.New("unknown remote command")
	ErrInvalidPayload      = errors.New("invalid remote command payload")
	ErrReasonRequired      = errors.New("reason is required")
	ErrNotHouseholdCommand = errors.New("command is not available to household members")
	ErrScopeMismatch       = errors.New("command does not apply to this scope")
)

// sessionCommands is the session-scoped table from spec §C. The value is
// whether the command needs a payload validator beyond "empty object".
var sessionCommands = map[playback.CommandName]struct{}{
	playback.CommandPause:            {},
	playback.CommandUnpause:          {},
	playback.CommandPlayPause:        {},
	playback.CommandSeek:             {},
	playback.CommandSetVolume:        {},
	playback.CommandStop:             {},
	playback.CommandTerminate:        {},
	playback.CommandSetAudioTrack:    {},
	playback.CommandSetSubtitleTrack: {},
	playback.CommandDisplayMessage:   {},
	CommandReplan:                    {},
}

// householdSessionCommands is the reduced set household members may send
// (spec §D). play_media is device-scoped and lands with S-5b.
var householdSessionCommands = map[playback.CommandName]struct{}{
	playback.CommandPause:            {},
	playback.CommandUnpause:          {},
	playback.CommandPlayPause:        {},
	playback.CommandSeek:             {},
	playback.CommandStop:             {},
	playback.CommandSetVolume:        {},
	playback.CommandSetAudioTrack:    {},
	playback.CommandSetSubtitleTrack: {},
	playback.CommandDisplayMessage:   {},
}

// SessionCommandNames lists the session-scoped names in a stable order.
func SessionCommandNames() []playback.CommandName {
	return []playback.CommandName{
		playback.CommandPause, playback.CommandUnpause, playback.CommandPlayPause,
		playback.CommandSeek, playback.CommandSetVolume, playback.CommandStop,
		playback.CommandTerminate, playback.CommandSetAudioTrack,
		playback.CommandSetSubtitleTrack, playback.CommandDisplayMessage, CommandReplan,
	}
}

// IsSessionCommand reports whether name rides the session rail.
func IsSessionCommand(name playback.CommandName) bool {
	_, ok := sessionCommands[name]
	return ok
}

// IsHouseholdSessionCommand reports whether household members may send name.
func IsHouseholdSessionCommand(name playback.CommandName) bool {
	_, ok := householdSessionCommands[name]
	return ok
}

// RequiresReason names the commands that must carry a non-empty reason (§F).
func RequiresReason(name playback.CommandName) bool {
	return name == playback.CommandTerminate
}

// SeekPayload is {position_ms}.
type SeekPayload struct {
	PositionMS int64 `json:"position_ms"`
}

// VolumePayload is {level: 0..100}.
type VolumePayload struct {
	Level int `json:"level"`
}

// ReasonPayload is {reason?} for stop / terminate.
type ReasonPayload struct {
	Reason string `json:"reason,omitempty"`
}

// TrackPayload is {track_id} or {off: true}.
type TrackPayload struct {
	TrackID string `json:"track_id,omitempty"`
	Off     bool   `json:"off,omitempty"`
}

// MessagePayload is {title, body, severity, timeout_ms?}.
type MessagePayload struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	Severity  string `json:"severity"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// TranscodeMode is the replan override for the route family.
type TranscodeMode string

const (
	TranscodeAuto   TranscodeMode = "auto"
	TranscodeForce  TranscodeMode = "force"
	TranscodeDirect TranscodeMode = "direct"
)

// ReplanOverrides pins planner inputs for a replan command (spec §C).
type ReplanOverrides struct {
	Transcode      TranscodeMode `json:"transcode"`
	MaxBitrateKbps int           `json:"max_bitrate_kbps,omitempty"`
	VideoCodec     string        `json:"video_codec,omitempty"`
	AudioCodec     string        `json:"audio_codec,omitempty"`
	Container      string        `json:"container,omitempty"`
}

// ReplanPayload is {overrides, reason}.
type ReplanPayload struct {
	Overrides ReplanOverrides `json:"overrides"`
	Reason    string          `json:"reason"`
}

// PlanOverrides converts the wire overrides into the planner's shape.
func (o ReplanOverrides) PlanOverrides() playback.PlanOverridesV3 {
	return playback.PlanOverridesV3{
		Transcode:      string(o.Transcode),
		MaxBitrateKbps: o.MaxBitrateKbps,
		VideoCodec:     strings.ToLower(strings.TrimSpace(o.VideoCodec)),
		AudioCodec:     strings.ToLower(strings.TrimSpace(o.AudioCodec)),
		Container:      strings.ToLower(strings.TrimSpace(o.Container)),
	}
}

const (
	maxMessageTitle = 120
	maxMessageBody  = 1000
	maxReasonLen    = 500
	maxTrackIDLen   = 128
	maxCodecLen     = 32
	maxBitrateKbps  = 1_000_000
)

var messageSeverities = map[string]struct{}{"info": {}, "warning": {}, "error": {}}

func strictDecode(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}
	if decoder.More() {
		return ErrInvalidPayload
	}
	return nil
}

func emptyPayload(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}
	if len(probe) != 0 {
		return fmt.Errorf("%w: %s takes no payload", ErrInvalidPayload, "command")
	}
	return nil
}

// ValidateSessionPayload checks a session-scoped command's payload against
// the spec table and returns the normalized payload the client receives.
// Unknown names and device-only names are rejected here, before any socket
// or store is touched.
func ValidateSessionPayload(name playback.CommandName, raw json.RawMessage, reason string) (json.RawMessage, error) {
	if !IsSessionCommand(name) {
		if err := playback.ValidateCommandName(name); err == nil {
			return nil, fmt.Errorf("%w: %s", ErrScopeMismatch, name)
		}
		return nil, fmt.Errorf("%w: %s", ErrUnknownCommand, name)
	}
	if len(reason) > maxReasonLen {
		return nil, fmt.Errorf("%w: reason exceeds %d characters", ErrInvalidPayload, maxReasonLen)
	}
	if RequiresReason(name) && strings.TrimSpace(reason) == "" {
		return nil, ErrReasonRequired
	}
	switch name {
	case playback.CommandPause, playback.CommandUnpause, playback.CommandPlayPause:
		if err := emptyPayload(raw); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case playback.CommandSeek:
		var p SeekPayload
		if err := strictDecode(raw, &p); err != nil {
			return nil, err
		}
		if p.PositionMS < 0 {
			return nil, fmt.Errorf("%w: position_ms must be >= 0", ErrInvalidPayload)
		}
		return marshal(p)
	case playback.CommandSetVolume:
		var p VolumePayload
		if err := strictDecode(raw, &p); err != nil {
			return nil, err
		}
		if p.Level < 0 || p.Level > 100 {
			return nil, fmt.Errorf("%w: level must be 0..100", ErrInvalidPayload)
		}
		return marshal(p)
	case playback.CommandStop, playback.CommandTerminate:
		var p ReasonPayload
		if err := strictDecode(raw, &p); err != nil {
			return nil, err
		}
		if p.Reason == "" {
			p.Reason = reason
		}
		if len(p.Reason) > maxReasonLen {
			return nil, fmt.Errorf("%w: reason exceeds %d characters", ErrInvalidPayload, maxReasonLen)
		}
		return marshal(p)
	case playback.CommandSetAudioTrack, playback.CommandSetSubtitleTrack:
		var p TrackPayload
		if err := strictDecode(raw, &p); err != nil {
			return nil, err
		}
		if p.Off == (p.TrackID != "") {
			return nil, fmt.Errorf("%w: exactly one of track_id or off is required", ErrInvalidPayload)
		}
		if len(p.TrackID) > maxTrackIDLen {
			return nil, fmt.Errorf("%w: track_id too long", ErrInvalidPayload)
		}
		if p.Off && name == playback.CommandSetAudioTrack {
			return nil, fmt.Errorf("%w: audio cannot be switched off", ErrInvalidPayload)
		}
		return marshal(p)
	case playback.CommandDisplayMessage:
		var p MessagePayload
		if err := strictDecode(raw, &p); err != nil {
			return nil, err
		}
		p.Title = strings.TrimSpace(p.Title)
		p.Body = strings.TrimSpace(p.Body)
		if p.Severity == "" {
			p.Severity = "info"
		}
		if _, ok := messageSeverities[p.Severity]; !ok {
			return nil, fmt.Errorf("%w: severity must be info, warning or error", ErrInvalidPayload)
		}
		if p.Body == "" || len(p.Body) > maxMessageBody || len(p.Title) > maxMessageTitle {
			return nil, fmt.Errorf("%w: body is required (max %d) and title max %d", ErrInvalidPayload, maxMessageBody, maxMessageTitle)
		}
		if p.TimeoutMS < 0 || p.TimeoutMS > 600_000 {
			return nil, fmt.Errorf("%w: timeout_ms must be 0..600000", ErrInvalidPayload)
		}
		return marshal(p)
	case CommandReplan:
		var p ReplanPayload
		if err := strictDecode(raw, &p); err != nil {
			return nil, err
		}
		if p.Reason == "" {
			p.Reason = reason
		}
		if err := ValidateReplanOverrides(p.Overrides); err != nil {
			return nil, err
		}
		if len(p.Reason) > maxReasonLen {
			return nil, fmt.Errorf("%w: reason exceeds %d characters", ErrInvalidPayload, maxReasonLen)
		}
		return marshal(p)
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownCommand, name)
}

// ValidateReplanOverrides checks the replan override block.
func ValidateReplanOverrides(o ReplanOverrides) error {
	switch o.Transcode {
	case TranscodeAuto, TranscodeForce, TranscodeDirect:
	case "":
		return fmt.Errorf("%w: overrides.transcode is required (auto|force|direct)", ErrInvalidPayload)
	default:
		return fmt.Errorf("%w: overrides.transcode must be auto, force or direct", ErrInvalidPayload)
	}
	if o.MaxBitrateKbps < 0 || o.MaxBitrateKbps > maxBitrateKbps {
		return fmt.Errorf("%w: max_bitrate_kbps must be 0..%d", ErrInvalidPayload, maxBitrateKbps)
	}
	for _, field := range []string{o.VideoCodec, o.AudioCodec, o.Container} {
		if len(field) > maxCodecLen {
			return fmt.Errorf("%w: codec/container names are at most %d characters", ErrInvalidPayload, maxCodecLen)
		}
		for _, r := range field {
			if !codecNameRune(r) {
				return fmt.Errorf("%w: codec/container names are alphanumeric", ErrInvalidPayload)
			}
		}
	}
	if o.Transcode == TranscodeDirect && (o.MaxBitrateKbps > 0 || o.VideoCodec != "" || o.AudioCodec != "") {
		return fmt.Errorf("%w: direct playback cannot pin a bitrate or codecs", ErrInvalidPayload)
	}
	return nil
}

func marshal(v any) (json.RawMessage, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}
	return data, nil
}

// codecNameRune accepts the characters a codec or container name may use.
func codecNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
}
