package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/ratelimit"
)

var (
	// ErrSessionNotConnected means the session exists but holds no live
	// control socket: session commands are never queued (spec §B.3).
	ErrSessionNotConnected = errors.New("session_not_connected")
	// ErrSessionNotFound mirrors playback.ErrSessionNotFound for callers.
	ErrSessionNotFound = playback.ErrSessionNotFound
	// ErrForbidden means the issuer may not control this session.
	ErrForbidden = errors.New("forbidden")
	// ErrRateLimited means the issuer exceeded the per-minute budget.
	ErrRateLimited = errors.New("rate_limited")
	// ErrReplanUnavailable means the session cannot be replanned in place
	// (no negotiated plan_invalidated support or no current plan).
	ErrReplanUnavailable = errors.New("replan_unavailable")
)

// SessionInfo is what the sender needs to know about a live session.
type SessionInfo struct {
	ID        string
	UserID    int
	ProfileID string
	DeviceID  string
	TenantID  string
	Connected bool
}

// Sender is the existing session-socket writer path, adapted by the API
// layer. The service never registers sockets itself.
type Sender interface {
	Session(sessionID string) (*SessionInfo, error)
	// Dispatch writes the command envelope to the live session socket and
	// arms whatever server-side fallback the command name implies (stop and
	// terminate tear the session down when the client never answers).
	Dispatch(ctx context.Context, command playback.CommandEnvelope) error
	// Replan pins overrides for the session and emits plan_invalidated with
	// commandID exactly as the copy-safety path does. It returns the
	// withdrawn plan id.
	Replan(ctx context.Context, sessionID, commandID string, overrides playback.PlanOverridesV3, reason string) (string, error)
}

// Config tunes the service.
type Config struct {
	// PerMinute is the per-issuer command budget (spec §F: 30).
	PerMinute int
	// CommandTTL bounds how long a sent command may stay unanswered before
	// the status endpoint reports it expired.
	CommandTTL time.Duration
}

// DefaultConfig is the spec's budget.
func DefaultConfig() Config {
	return Config{PerMinute: 30, CommandTTL: 60 * time.Second}
}

// Service is the session-scoped sender.
type Service struct {
	store   Store
	sender  Sender
	limiter ratelimit.RateLimiter
	cfg     Config
	now     func() time.Time
}

// NewService wires a service. A nil limiter gets a private memory limiter.
func NewService(store Store, sender Sender, limiter ratelimit.RateLimiter, cfg Config) *Service {
	if cfg.PerMinute <= 0 {
		cfg.PerMinute = DefaultConfig().PerMinute
	}
	if cfg.CommandTTL <= 0 {
		cfg.CommandTTL = DefaultConfig().CommandTTL
	}
	if limiter == nil {
		limiter = ratelimit.NewMemoryLimiter()
	}
	return &Service{store: store, sender: sender, limiter: limiter, cfg: cfg, now: time.Now}
}

// SetClock overrides the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// SendInput is one command request.
type SendInput struct {
	SessionID  string
	Name       playback.CommandName
	Payload    json.RawMessage
	Reason     string
	IssuerKind IssuerKind
	IssuedBy   string
	// TenantScope, when set, restricts admin issuers to sessions of that
	// tenant. Empty means global scope.
	TenantScope string
	// HouseholdUserID is the account whose sessions a household issuer may
	// control.
	HouseholdUserID int
}

// SendToSession validates, authorizes, rate-limits, delivers and records one
// command. It returns the stored row; a device that did not advertise the
// command yields a row in StateRejectedUnsupported and no error.
func (s *Service) SendToSession(ctx context.Context, in SendInput) (*Command, error) {
	if s == nil || s.store == nil || s.sender == nil {
		return nil, errors.New("remote control is not configured")
	}
	if in.IssuedBy == "" || in.SessionID == "" {
		return nil, fmt.Errorf("%w: issuer and session are required", ErrInvalidPayload)
	}
	if in.IssuerKind == IssuerHousehold && !IsHouseholdSessionCommand(in.Name) {
		if IsSessionCommand(in.Name) {
			return nil, fmt.Errorf("%w: %s", ErrNotHouseholdCommand, in.Name)
		}
		return nil, fmt.Errorf("%w: %s", ErrUnknownCommand, in.Name)
	}
	payload, err := ValidateSessionPayload(in.Name, in.Payload, in.Reason)
	if err != nil {
		return nil, err
	}
	session, err := s.sender.Session(in.SessionID)
	if err != nil {
		return nil, err
	}
	switch in.IssuerKind {
	case IssuerHousehold:
		if in.HouseholdUserID <= 0 || session.UserID != in.HouseholdUserID {
			return nil, ErrForbidden
		}
	case IssuerAdmin:
		if in.TenantScope != "" && session.TenantID != in.TenantScope {
			return nil, ErrForbidden
		}
	default:
		return nil, fmt.Errorf("%w: unknown issuer kind", ErrInvalidPayload)
	}
	if !s.allow(ctx, in) {
		return nil, ErrRateLimited
	}

	now := s.now().UTC()
	expires := now.Add(s.cfg.CommandTTL)
	command := &Command{
		ID:              uuid.NewString(),
		Scope:           ScopeSession,
		TargetSessionID: session.ID,
		TargetDeviceID:  session.DeviceID,
		TargetUserID:    session.UserID,
		TargetProfileID: session.ProfileID,
		TenantID:        session.TenantID,
		Name:            in.Name,
		Payload:         payload,
		IssuedBy:        in.IssuedBy,
		IssuerKind:      in.IssuerKind,
		Reason:          strings.TrimSpace(in.Reason),
		CreatedAt:       now,
		ExpiresAt:       &expires,
	}

	capability, err := s.deviceCapability(ctx, session)
	if err != nil {
		return nil, err
	}
	if !capability.Supports(in.Name) {
		command.State = StateRejectedUnsupported
		command.FinishedAt = &now
		command.Error = "device did not advertise " + string(in.Name)
		if err := s.store.Insert(ctx, command); err != nil {
			return nil, err
		}
		return command, nil
	}
	if !session.Connected {
		return nil, ErrSessionNotConnected
	}

	command.State = StateSent
	command.SentAt = &now
	if err := s.store.Insert(ctx, command); err != nil {
		return nil, err
	}

	var deliverErr error
	if in.Name == CommandReplan {
		var replan ReplanPayload
		_ = json.Unmarshal(payload, &replan)
		_, deliverErr = s.sender.Replan(ctx, session.ID, command.ID, replan.Overrides.PlanOverrides(), replan.Reason)
	} else {
		env, buildErr := playback.NewCommandEnvelope(session.ID, command.ID, in.Name, payload)
		if buildErr != nil {
			deliverErr = buildErr
		} else {
			env.Reason = command.Reason
			env.IssuedBy = &playback.CommandIssuedBy{Kind: string(in.IssuerKind)}
			deliverErr = s.sender.Dispatch(ctx, env)
		}
	}
	if deliverErr != nil {
		state, errText := StateFailed, deliverErr.Error()
		if errors.Is(deliverErr, playback.ErrRealtimeConnectionNotFound) || errors.Is(deliverErr, ErrSessionNotConnected) {
			errText = ErrSessionNotConnected.Error()
			deliverErr = ErrSessionNotConnected
		}
		if _, err := s.store.Transition(ctx, command.ID, state, nil, errText, s.now().UTC()); err != nil {
			slog.WarnContext(ctx, "remote command failure not recorded", "component", "remote", "command_id", command.ID, "error", err)
		}
		return nil, deliverErr
	}
	return command, nil
}

func (s *Service) allow(ctx context.Context, in SendInput) bool {
	if s.limiter == nil {
		return true
	}
	key := "remote:" + string(in.IssuerKind) + ":" + in.IssuedBy
	perMinute := float64(s.cfg.PerMinute)
	result := s.limiter.Allow(ctx, key, ratelimit.Rate{RequestsPerSecond: perMinute, RequestsPerMinute: perMinute, Burst: s.cfg.PerMinute})
	return result.Allowed
}

func (s *Service) deviceCapability(ctx context.Context, session *SessionInfo) (*DeviceCapability, error) {
	if session.DeviceID == "" {
		return nil, nil
	}
	return s.store.GetDeviceCapability(ctx, session.UserID, session.ProfileID, session.DeviceID)
}

// Get returns a command, marking an unanswered one expired once its TTL has
// passed.
func (s *Service) Get(ctx context.Context, id string) (*Command, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("remote control is not configured")
	}
	command, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if !command.State.Terminal() && command.ExpiresAt != nil && now.After(*command.ExpiresAt) {
		if _, err := s.store.Transition(ctx, id, StateExpired, nil, "no acknowledgement before expiry", now); err == nil {
			command.State = StateExpired
			command.FinishedAt = &now
			command.Error = "no acknowledgement before expiry"
		}
	}
	return command, nil
}

// ListAudit pages the audit log.
func (s *Service) ListAudit(ctx context.Context, query AuditQuery) ([]Command, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("remote control is not configured")
	}
	return s.store.ListAudit(ctx, query)
}

// AdvertiseDevice persists a device's remote_control block (§A).
func (s *Service) AdvertiseDevice(ctx context.Context, userID int, profileID, deviceID string, version int, commands []playback.CommandName) error {
	if s == nil || s.store == nil {
		return errors.New("remote control is not configured")
	}
	if userID <= 0 || strings.TrimSpace(profileID) == "" || strings.TrimSpace(deviceID) == "" {
		return fmt.Errorf("%w: user, profile and device are required", ErrInvalidPayload)
	}
	if version <= 0 {
		version = CapabilityVersion
	}
	for _, name := range commands {
		if !IsAdvertisableCommand(name) {
			return fmt.Errorf("%w: %s", ErrUnknownCommand, name)
		}
	}
	return s.store.UpsertDeviceCapability(ctx, DeviceCapability{UserID: userID, ProfileID: profileID, DeviceID: deviceID, Version: version, Commands: commands})
}

// DeviceCapability reads a device's persisted block; nil when absent.
func (s *Service) DeviceCapability(ctx context.Context, userID int, profileID, deviceID string) (*DeviceCapability, error) {
	if s == nil || s.store == nil || deviceID == "" {
		return nil, nil
	}
	return s.store.GetDeviceCapability(ctx, userID, profileID, deviceID)
}

// deviceOnlyCommand accepts the S-5b device-rail names in an advertisement so
// a v3 client can announce its full list today.
func deviceOnlyCommand(name playback.CommandName) bool {
	switch name {
	case "collect_diagnostics", "refresh_settings", "sign_out":
		return true
	}
	return false
}

// IsAdvertisableCommand reports whether a device may list name in its
// remote_control block: every session-rail name, the S-5b device-rail names,
// and the upstream socket vocabulary.
func IsAdvertisableCommand(name playback.CommandName) bool {
	return IsSessionCommand(name) || deviceOnlyCommand(name) || playback.ValidateCommandName(name) == nil
}

// IsRemoteOnlyCommand reports whether name is a remote control name the
// upstream socket vocabulary does not know (replan, the device-rail names).
// The socket hello validator is upstream code; the API layer strips these
// before running it and hands the full list to OnHello.
func IsRemoteOnlyCommand(name playback.CommandName) bool {
	return IsAdvertisableCommand(name) && playback.ValidateCommandName(name) != nil
}

// ForgetDevice drops a device's remote_control block; the device registry
// forget path calls it so a forgotten device is not controllable.
func (s *Service) ForgetDevice(ctx context.Context, userID int, profileID, deviceID string) error {
	if s == nil || s.store == nil {
		return errors.New("remote control is not configured")
	}
	return s.store.DeleteDeviceCapability(ctx, userID, profileID, deviceID)
}

// replanPinStates are the states in which a replan command keeps its
// overrides pinned on the session: from the moment it is sent until the
// session ends. done is included on purpose: the pin must outlive the
// client's first replan so later replans (seek re-anchor, recovery) keep the
// admin's plan. rejected / expired / failed release it.
var replanPinStates = []State{StateSent, StateAccepted, StateDone}

// OpenReplanOverrides returns the overrides pinned on a session by its newest
// live replan command. The pin is read from the store, not from memory, so an
// instance that never delivered the command applies it too.
func (s *Service) OpenReplanOverrides(ctx context.Context, sessionID string) (playback.PlanOverridesV3, bool) {
	if s == nil || s.store == nil || sessionID == "" {
		return playback.PlanOverridesV3{}, false
	}
	command, err := s.store.LatestSessionCommand(ctx, sessionID, CommandReplan, replanPinStates)
	if err != nil {
		slog.WarnContext(ctx, "remote replan pin not read", "component", "remote", "session_id", sessionID, "error", err)
		return playback.PlanOverridesV3{}, false
	}
	if command == nil {
		return playback.PlanOverridesV3{}, false
	}
	if !command.State.Terminal() && command.ExpiresAt != nil && s.now().UTC().After(*command.ExpiresAt) {
		return playback.PlanOverridesV3{}, false
	}
	var payload ReplanPayload
	if err := json.Unmarshal(command.Payload, &payload); err != nil {
		slog.WarnContext(ctx, "remote replan pin payload unreadable", "component", "remote", "command_id", command.ID, "error", err)
		return playback.PlanOverridesV3{}, false
	}
	return payload.Overrides.PlanOverrides(), true
}

// OnSessionEnded finalizes every command still open on a session that has
// ended: nothing can answer them any more.
func (s *Service) OnSessionEnded(ctx context.Context, sessionID string) {
	if s == nil || s.store == nil || sessionID == "" {
		return
	}
	if _, err := s.store.TransitionOpenSessionCommands(ctx, sessionID, "", StateExpired, nil, "session ended before the command completed", s.now().UTC()); err != nil {
		slog.WarnContext(ctx, "remote commands not finalized at session end", "component", "remote", "session_id", sessionID, "error", err)
	}
}

// OnHello records the command list a client announced on its session
// socket against the device it plays from. An empty list (every v2 client)
// leaves the device untouched: absence, not an empty row, is what marks a
// device not controllable.
func (s *Service) OnHello(ctx context.Context, session SessionInfo, commands []playback.CommandName) {
	if s == nil || s.store == nil || session.DeviceID == "" || len(commands) == 0 {
		return
	}
	if err := s.AdvertiseDevice(ctx, session.UserID, session.ProfileID, session.DeviceID, CapabilityVersion, commands); err != nil {
		slog.WarnContext(ctx, "remote control hello capabilities not recorded", "component", "remote", "session_id", session.ID, "error", err)
	}
}

// OnAck moves a sent command to accepted.
func (s *Service) OnAck(ctx context.Context, commandID string) {
	if s == nil || s.store == nil {
		return
	}
	if _, err := s.store.Transition(ctx, commandID, StateAccepted, nil, "", s.now().UTC()); err != nil {
		slog.WarnContext(ctx, "remote command ack not recorded", "component", "remote", "command_id", commandID, "error", err)
	}
}

// OnResult finishes a command from the client's result frame.
func (s *Service) OnResult(ctx context.Context, commandID string, completed bool, errText string) {
	if s == nil || s.store == nil {
		return
	}
	state := StateDone
	var result json.RawMessage
	if completed {
		result = json.RawMessage(`{"status":"completed"}`)
	} else {
		state = StateRejected
		result = json.RawMessage(`{"status":"rejected"}`)
	}
	if _, err := s.store.Transition(ctx, commandID, state, result, errText, s.now().UTC()); err != nil {
		slog.WarnContext(ctx, "remote command result not recorded", "component", "remote", "command_id", commandID, "error", err)
	}
}

// OnDeadline marks a command the client never answered before the server's
// fallback fired.
func (s *Service) OnDeadline(ctx context.Context, commandID string) {
	if s == nil || s.store == nil {
		return
	}
	if _, err := s.store.Transition(ctx, commandID, StateExpired, nil, "no acknowledgement before the command deadline", s.now().UTC()); err != nil {
		slog.WarnContext(ctx, "remote command expiry not recorded", "component", "remote", "command_id", commandID, "error", err)
	}
}

// OnSessionReplanned completes every open replan command on a session once
// the client has fetched its replacement plan.
func (s *Service) OnSessionReplanned(ctx context.Context, sessionID, planID string) {
	if s == nil || s.store == nil || sessionID == "" {
		return
	}
	result, _ := json.Marshal(map[string]string{"status": "completed", "plan_id": planID})
	if _, err := s.store.TransitionOpenSessionCommands(ctx, sessionID, CommandReplan, StateDone, result, "", s.now().UTC()); err != nil {
		slog.WarnContext(ctx, "remote replan completion not recorded", "component", "remote", "session_id", sessionID, "error", err)
	}
}
