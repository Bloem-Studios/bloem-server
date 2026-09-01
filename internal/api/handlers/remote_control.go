package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remote"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// PlanInvalidatedAdminReplan is the plan_invalidated reason an admin replan
// carries. Clients treat the reason as opaque and act on plan_id alone.
const PlanInvalidatedAdminReplan = "admin_replan"

// RemoteObserver receives the session socket's frames for the remote
// control audit (S-5a). Every method is optional to call and safe on nil.
type RemoteObserver interface {
	OnHello(ctx context.Context, session remote.SessionInfo, commands []playback.CommandName)
	OnAck(ctx context.Context, commandID string)
	OnResult(ctx context.Context, commandID string, completed bool, errText string)
	OnDeadline(ctx context.Context, commandID string)
	OnSessionReplanned(ctx context.Context, sessionID, planID string)
	// OnSessionEnded finalizes whatever is still open on a session that ended.
	OnSessionEnded(ctx context.Context, sessionID string)
	// OpenReplanOverrides returns the overrides an admin replan pinned on a
	// session, read from durable state so every instance applies them.
	OpenReplanOverrides(ctx context.Context, sessionID string) (playback.PlanOverridesV3, bool)
}

func remoteSessionInfo(session *playback.Session) remote.SessionInfo {
	return remote.SessionInfo{
		ID:        session.ID,
		UserID:    session.UserID,
		ProfileID: session.ProfileID,
		DeviceID:  session.DeviceID,
		TenantID:  session.TenantID,
		Connected: session.HasRealtimeConnection,
	}
}

// remoteCommandDeadline is how long a remote command may wait for an ack
// before the server-side fallback fires.
func (h *PlaybackHandler) remoteCommandDeadline() time.Duration {
	if h != nil && h.RemoteCommandDeadline > 0 {
		return h.RemoteCommandDeadline
	}
	return playback.CopySafetyInvalidationDeadline
}

// remotePlanOverridesV3 returns the overrides an admin replan pinned on a
// session. The pin is the session's newest live replan command row in the
// remote store, so an instance that never delivered the command applies it
// too; only the socket delivery itself is per-instance.
func (h *PlaybackHandler) remotePlanOverridesV3(ctx context.Context, sessionID string) (playback.PlanOverridesV3, bool) {
	if h == nil || h.RemoteObserver == nil || sessionID == "" {
		return playback.PlanOverridesV3{}, false
	}
	return h.RemoteObserver.OpenReplanOverrides(ctx, sessionID)
}

// applyRemotePlanOverridesV3 narrows a replan's start request by the pinned
// overrides and returns them for the planner's ForceTranscode input. The
// durable attempt record is never rewritten.
func (h *PlaybackHandler) applyRemotePlanOverridesV3(ctx context.Context, sessionID string, start playback.StartRequestV3) (playback.StartRequestV3, playback.PlanOverridesV3) {
	overrides, ok := h.remotePlanOverridesV3(ctx, sessionID)
	if !ok {
		return start, playback.PlanOverridesV3{}
	}
	return playback.ApplyPlanOverridesV3(start, overrides), overrides
}

func (h *PlaybackHandler) notifyRemoteSessionEnded(ctx context.Context, sessionID string) {
	if h == nil || h.RemoteObserver == nil || sessionID == "" {
		return
	}
	h.RemoteObserver.OnSessionEnded(ctx, sessionID)
}

func (h *PlaybackHandler) notifyRemoteSessionReplanned(ctx context.Context, sessionID string, plan *playback.PlanV3) {
	if h == nil || h.RemoteObserver == nil || plan == nil {
		return
	}
	h.RemoteObserver.OnSessionReplanned(ctx, sessionID, plan.PlanID)
}

// playbackRemoteSender adapts the playback handler's existing socket path
// (hub, dispatcher, tracker, command bookkeeping) to remote.Sender.
type playbackRemoteSender struct {
	playback *PlaybackHandler
}

// NewPlaybackRemoteSender returns the remote.Sender over a playback handler.
func NewPlaybackRemoteSender(handler *PlaybackHandler) remote.Sender {
	if handler == nil {
		return nil
	}
	return &playbackRemoteSender{playback: handler}
}

func (s *playbackRemoteSender) Session(sessionID string) (*remote.SessionInfo, error) {
	if s == nil || s.playback == nil || s.playback.sessionMgr == nil {
		return nil, playback.ErrSessionNotFound
	}
	session, err := s.playback.sessionMgr.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	info := remoteSessionInfo(session)
	return &info, nil
}

func (s *playbackRemoteSender) Dispatch(ctx context.Context, command playback.CommandEnvelope) error {
	if s == nil || s.playback == nil || s.playback.CommandDispatcher == nil {
		return playback.ErrCommandDispatchUnavailable
	}
	h := s.playback
	sessionID, commandID := command.SessionID, command.CommandID
	deadline := h.remoteCommandDeadline()
	command.DeadlineMS = int(deadline / time.Millisecond)
	tearDown := command.Name == playback.CommandStop || command.Name == playback.CommandTerminate
	fallback := func() {
		h.forgetRealtimeCommand(commandID)
		if h.RemoteObserver != nil {
			h.RemoteObserver.OnDeadline(context.WithoutCancel(ctx), commandID)
		}
		if tearDown {
			_ = h.stopPlaybackSessionByID(context.WithoutCancel(ctx), sessionID, true)
		}
	}
	h.rememberRealtimeCommand(commandID, sessionID, command.Name)
	result := h.CommandDispatcher.DispatchToSession(command, deadline, fallback)
	if result.DispatchErr != nil {
		h.forgetRealtimeCommand(commandID)
		return result.DispatchErr
	}
	return nil
}

// Replan withdraws the session's current plan with a plan_invalidated
// command, exactly as the copy-safety notifier does. The overrides themselves
// are not held here: the client's replan reads them from the command row
// (OpenReplanOverrides), so any instance serving that replan applies them.
func (s *playbackRemoteSender) Replan(ctx context.Context, sessionID, commandID string, _ playback.PlanOverridesV3, reason string) (string, error) {
	if s == nil || s.playback == nil || s.playback.CommandDispatcher == nil {
		return "", playback.ErrCommandDispatchUnavailable
	}
	h := s.playback
	session, err := h.sessionMgr.GetSession(sessionID)
	if err != nil {
		return "", err
	}
	if !session.HasRealtimeConnection {
		return "", remote.ErrSessionNotConnected
	}
	if h.PlanStoreV3 == nil {
		return "", remote.ErrReplanUnavailable
	}
	record, err := h.PlanStoreV3.GetAttempt(ctx, sessionID)
	if err != nil || record == nil {
		return "", remote.ErrReplanUnavailable
	}
	if !playback.HasFeatureV3(record.NormalizedRequest.ClientFeatures, playback.FeaturePlanInvalidatedV3) {
		return "", remote.ErrReplanUnavailable
	}
	planID := record.CurrentPlan.PlanID
	if planID == "" {
		planID = record.CurrentPlanID
	}
	if planID == "" {
		return "", remote.ErrReplanUnavailable
	}
	command, err := playback.NewPlanInvalidatedCommand(sessionID, commandID, planID, PlanInvalidatedAdminReplan)
	if err != nil {
		return "", err
	}
	command.Reason = reason
	command.IssuedBy = &playback.CommandIssuedBy{Kind: string(remote.IssuerAdmin)}
	deadline := h.remoteCommandDeadline()
	command.DeadlineMS = int(deadline / time.Millisecond)

	fallback := func() {
		// The client never answered: the withdrawn plan is still running, so
		// end the session the way an unnegotiated client is ended (system
		// teardown keeps the recipe card for recovery). OnDeadline expires the
		// command row, which releases the pin.
		h.forgetRealtimeCommand(commandID)
		if h.RemoteObserver != nil {
			h.RemoteObserver.OnDeadline(context.WithoutCancel(ctx), commandID)
		}
		_ = h.stopPlaybackSessionByID(context.WithoutCancel(ctx), sessionID, false)
	}
	h.rememberRealtimeCommand(commandID, sessionID, playback.CommandPlanInvalidated)
	result := h.CommandDispatcher.DispatchToSession(command, deadline, fallback)
	if result.DispatchErr != nil {
		h.forgetRealtimeCommand(commandID)
		return "", result.DispatchErr
	}
	return planID, nil
}

// RemoteControlHandler serves the S-5a sender endpoints.
type RemoteControlHandler struct {
	service   *remote.Service
	playback  *PlaybackHandler
	profiles  *ProfileHandler
	storeProv userstore.UserStoreProvider
	// SessionsLoader, when set, enriches the admin session listing with the
	// catalog and node joins the admin sessions page already uses.
	SessionsLoader playbackSessionsReader
}

// NewRemoteControlHandler wires the handler. profiles may be nil when the
// household route is not mounted.
func NewRemoteControlHandler(service *remote.Service, playbackHandler *PlaybackHandler, profiles *ProfileHandler, storeProv userstore.UserStoreProvider) *RemoteControlHandler {
	if service == nil || playbackHandler == nil {
		return nil
	}
	return &RemoteControlHandler{service: service, playback: playbackHandler, profiles: profiles, storeProv: storeProv}
}

type remoteCommandRequest struct {
	Name    playback.CommandName `json:"name"`
	Payload json.RawMessage      `json:"payload,omitempty"`
	Reason  string               `json:"reason,omitempty"`
	// TTLSeconds belongs to the device rail (S-5b). Session commands are
	// never queued, so a non-zero value is refused rather than ignored.
	TTLSeconds int `json:"ttl_seconds,omitempty"`
}

type remoteCapabilityRequest struct {
	Version  int                    `json:"version"`
	Commands []playback.CommandName `json:"commands"`
}

type remoteCapabilityResponse struct {
	DeviceID string                 `json:"device_id"`
	Version  int                    `json:"version"`
	Commands []playback.CommandName `json:"commands"`
}

type remoteSessionControl struct {
	DeviceID     string                 `json:"device_id,omitempty"`
	Connected    bool                   `json:"connected"`
	Controllable bool                   `json:"controllable"`
	Commands     []playback.CommandName `json:"commands"`
}

type remotePlanSummary struct {
	PlayMethod          string  `json:"play_method"`
	EffectivePlayMethod string  `json:"effective_play_method,omitempty"`
	VideoDecision       string  `json:"video_decision,omitempty"`
	AudioDecision       string  `json:"audio_decision,omitempty"`
	SourceContainer     string  `json:"source_container,omitempty"`
	SourceVideoCodec    string  `json:"source_video_codec,omitempty"`
	SourceAudioCodec    string  `json:"source_audio_codec,omitempty"`
	TargetVideoCodec    string  `json:"target_video_codec,omitempty"`
	TargetAudioCodec    string  `json:"target_audio_codec,omitempty"`
	TargetResolution    string  `json:"target_resolution,omitempty"`
	TargetBitrateKbps   *int    `json:"target_bitrate_kbps,omitempty"`
	StreamBitrateKbps   *int    `json:"stream_bitrate_kbps,omitempty"`
	IsPaused            bool    `json:"is_paused"`
	PositionSeconds     float64 `json:"position_seconds"`
}

type remoteSessionRow struct {
	playbackSessionRow
	RemoteControl remoteSessionControl `json:"remote_control"`
	PlanSummary   remotePlanSummary    `json:"plan_summary"`
}

type remoteSessionsResponse struct {
	Sessions []remoteSessionRow `json:"sessions"`
}

type remoteAuditResponse struct {
	Commands []remote.Command `json:"commands"`
	Limit    int              `json:"limit"`
	Offset   int              `json:"offset"`
}

func writeRemoteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, remote.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Playback session not found")
	case errors.Is(err, remote.ErrSessionNotConnected):
		writeError(w, http.StatusConflict, "session_not_connected", "The playback session has no live control connection")
	case errors.Is(err, remote.ErrReplanUnavailable):
		writeError(w, http.StatusConflict, "replan_unavailable", "This client cannot replan in place")
	case errors.Is(err, remote.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many commands; try again in a minute")
	case errors.Is(err, remote.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "This session is outside your control scope")
	case errors.Is(err, remote.ErrNotHouseholdCommand):
		writeError(w, http.StatusForbidden, "command_not_allowed", "Household members cannot send this command")
	case errors.Is(err, remote.ErrReasonRequired):
		writeError(w, http.StatusBadRequest, "reason_required", "A reason is required for this command")
	case errors.Is(err, remote.ErrUnknownCommand), errors.Is(err, remote.ErrScopeMismatch):
		writeError(w, http.StatusBadRequest, "unknown_command", "Unknown or non-session command name")
	case errors.Is(err, remote.ErrInvalidPayload):
		writeError(w, http.StatusBadRequest, "invalid_payload", err.Error())
	case errors.Is(err, remote.ErrCommandNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Command not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Remote control request failed")
	}
}

func decodeRemoteCommandRequest(w http.ResponseWriter, r *http.Request) (remoteCommandRequest, bool) {
	var req remoteCommandRequest
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Request body is required")
		return req, false
	}
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return req, false
	}
	if strings.TrimSpace(string(req.Name)) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Command name is required")
		return req, false
	}
	if req.TTLSeconds != 0 {
		writeError(w, http.StatusBadRequest, "invalid_payload", "ttl_seconds applies to device commands only; session commands are never queued")
		return req, false
	}
	return req, true
}

// HandleAdminSendSessionCommand handles POST /admin/remote/sessions/{session_id}/commands.
func (h *RemoteControlHandler) HandleAdminSendSessionCommand(w http.ResponseWriter, r *http.Request) {
	claims := apimw.GetClaims(r.Context())
	if claims == nil || claims.UserID == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	sessionID := chi.URLParam(r, "session_id")
	req, ok := decodeRemoteCommandRequest(w, r)
	if !ok {
		return
	}
	in := remote.SendInput{
		SessionID:  sessionID,
		Name:       req.Name,
		Payload:    req.Payload,
		Reason:     req.Reason,
		IssuerKind: remote.IssuerAdmin,
		IssuedBy:   "user:" + strconv.Itoa(claims.UserID),
	}
	if org := adminResourceOrganization(r.Context()); org != uuid.Nil {
		in.TenantScope = org.String()
	}
	command, err := h.service.SendToSession(r.Context(), in)
	if err != nil {
		writeRemoteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, command)
}

// HandleHouseholdSendSessionCommand handles POST /profiles/household/sessions/{session_id}/commands.
func (h *RemoteControlHandler) HandleHouseholdSendSessionCommand(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	if h.profiles == nil || h.storeProv == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Household control is unavailable")
		return
	}
	store, err := h.storeProv.ForUser(r.Context(), userID)
	if err != nil || store == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}
	allowed, err := h.profiles.canManageHouseholdProfiles(r, store)
	if err != nil {
		writeProfileManagementPermissionError(w, err)
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Household control requires the primary profile or admin access")
		return
	}
	sessionID := chi.URLParam(r, "session_id")
	req, ok := decodeRemoteCommandRequest(w, r)
	if !ok {
		return
	}
	issuedBy := "user:" + strconv.Itoa(userID)
	if profileID := apimw.GetProfileID(r.Context()); profileID != "" {
		issuedBy = "profile:" + profileID
	}
	command, err := h.service.SendToSession(r.Context(), remote.SendInput{
		SessionID:       sessionID,
		Name:            req.Name,
		Payload:         req.Payload,
		Reason:          req.Reason,
		IssuerKind:      remote.IssuerHousehold,
		IssuedBy:        issuedBy,
		HouseholdUserID: userID,
	})
	if err != nil {
		writeRemoteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, command)
}

// HandleGetCommand handles GET /admin/remote/commands/{command_id}.
func (h *RemoteControlHandler) HandleGetCommand(w http.ResponseWriter, r *http.Request) {
	command, err := h.service.Get(r.Context(), chi.URLParam(r, "command_id"))
	if err != nil {
		writeRemoteError(w, err)
		return
	}
	if org := adminResourceOrganization(r.Context()); org != uuid.Nil && command.TenantID != org.String() {
		writeError(w, http.StatusNotFound, "not_found", "Command not found")
		return
	}
	writeJSON(w, http.StatusOK, command)
}

// HandleListAudit handles GET /admin/remote/audit.
func (h *RemoteControlHandler) HandleListAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePagination(r)
	q := r.URL.Query()
	query := remote.AuditQuery{
		SessionID:  strings.TrimSpace(q.Get("session_id")),
		IssuedBy:   strings.TrimSpace(q.Get("issued_by")),
		IssuerKind: remote.IssuerKind(strings.TrimSpace(q.Get("issuer_kind"))),
		Limit:      limit,
		Offset:     offset,
	}
	if org := adminResourceOrganization(r.Context()); org != uuid.Nil {
		query.TenantID = org.String()
	}
	commands, err := h.service.ListAudit(r.Context(), query)
	if err != nil {
		writeRemoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, remoteAuditResponse{Commands: commands, Limit: limit, Offset: offset})
}

// HandleAdvertiseDevice handles PUT /devices/{device_id}/remote-control: the
// capability handshake (spec §A) for the calling profile's device.
func (h *RemoteControlHandler) HandleAdvertiseDevice(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	profileID := apimw.GetProfileID(r.Context())
	deviceID := strings.TrimSpace(chi.URLParam(r, "device_id"))
	if userID == 0 || profileID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication and profile are required")
		return
	}
	if deviceID == "" || len(deviceID) > 128 {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid device id")
		return
	}
	var req remoteCapabilityRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	// Only a device this profile registered may advertise. 404 rather than
	// 403, as the device routes do: a 403 would confirm the id exists.
	if h.storeProv == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Device registry is unavailable")
		return
	}
	store, err := h.storeProv.ForUser(r.Context(), userID)
	if err != nil || store == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access user store")
		return
	}
	registry, isRegistry := store.(userstore.DeviceRegistry)
	if !isRegistry {
		writeError(w, http.StatusNotFound, "not_found", "Device not found")
		return
	}
	owned, err := registry.DeviceExists(r.Context(), profileID, deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to look up device")
		return
	}
	if !owned {
		writeError(w, http.StatusNotFound, "not_found", "Device not found")
		return
	}
	if err := h.service.AdvertiseDevice(r.Context(), userID, profileID, deviceID, req.Version, req.Commands); err != nil {
		writeRemoteError(w, err)
		return
	}
	capability, err := h.service.DeviceCapability(r.Context(), userID, profileID, deviceID)
	if err != nil || capability == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read device capabilities")
		return
	}
	writeJSON(w, http.StatusOK, remoteCapabilityResponse{DeviceID: deviceID, Version: capability.Version, Commands: capability.Commands})
}

// HandleListSessions handles GET /admin/remote/sessions.
func (h *RemoteControlHandler) HandleListSessions(w http.ResponseWriter, r *http.Request) {
	if h.playback == nil || h.playback.sessionMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Playback sessions are unavailable")
		return
	}
	live := map[string]*playback.Session{}
	if lister, ok := h.playback.sessionMgr.(interface{ AllSessions() []*playback.Session }); ok {
		for _, session := range lister.AllSessions() {
			if session != nil {
				live[session.ID] = session
			}
		}
	}
	var rows []playbackSessionRow
	if h.SessionsLoader != nil {
		loaded, err := h.SessionsLoader.Load(r.Context(), r, PlaybackSessionsQuery{})
		if err != nil {
			slog.ErrorContext(r.Context(), "remote control session listing failed", "component", "api", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list sessions")
			return
		}
		rows = loaded
	} else {
		for _, session := range live {
			rows = append(rows, playbackSessionRowFromMemory(session))
		}
	}
	var tenantScope string
	if org := adminResourceOrganization(r.Context()); org != uuid.Nil {
		tenantScope = org.String()
	}
	response := remoteSessionsResponse{Sessions: []remoteSessionRow{}}
	for _, row := range rows {
		session := live[row.SessionID]
		if tenantScope != "" && (session == nil || session.TenantID != tenantScope) {
			continue
		}
		out := remoteSessionRow{playbackSessionRow: row, RemoteControl: remoteSessionControl{Commands: []playback.CommandName{}}}
		out.PlanSummary = remotePlanSummary{
			PlayMethod: row.PlayMethod, EffectivePlayMethod: row.EffectivePlayMethod,
			VideoDecision: row.VideoDecision, AudioDecision: row.AudioDecision,
			SourceContainer: row.SourceContainer, SourceVideoCodec: row.SourceVideoCodec, SourceAudioCodec: row.SourceAudioCodec,
			TargetVideoCodec: row.TargetVideoCodec, TargetAudioCodec: row.TargetAudioCodec, TargetResolution: row.TargetResolution,
			TargetBitrateKbps: row.TargetBitrateKbps, StreamBitrateKbps: row.StreamBitrateKbps,
			IsPaused: row.IsPaused, PositionSeconds: row.PositionSeconds,
		}
		if session != nil {
			out.RemoteControl.DeviceID = session.DeviceID
			out.RemoteControl.Connected = session.HasRealtimeConnection
			if capability, err := h.service.DeviceCapability(r.Context(), session.UserID, session.ProfileID, session.DeviceID); err == nil && capability != nil {
				out.RemoteControl.Controllable = len(capability.Commands) > 0
				out.RemoteControl.Commands = capability.Commands
			}
		}
		response.Sessions = append(response.Sessions, out)
	}
	writeJSON(w, http.StatusOK, response)
}

// playbackSessionRowFromMemory builds the listing row for a live session when
// no database-backed loader is wired.
func playbackSessionRowFromMemory(session *playback.Session) playbackSessionRow {
	row := playbackSessionRow{
		SessionID:            session.ID,
		UserID:               session.UserID,
		ProfileID:            session.ProfileID,
		MediaFileID:          session.MediaFileID,
		RequestedMediaFileID: session.RequestedMediaFileID,
		PlayMethod:           string(session.PlayMethod),
		StartedAt:            session.StartedAt,
		UpdatedAt:            session.UpdatedAt,
		PositionSeconds:      session.Position,
		IsPaused:             session.IsPaused,
		HasPlaybackControl:   session.HasWebSocket,
		ClientIP:             session.ClientIP,
		ClientName:           session.ClientName,
		ClientVersion:        session.ClientVersion,
		ClientBuild:          session.ClientBuild,
		ClientChannel:        session.ClientChannel,
		ClientUserAgent:      session.ClientUserAgent,
		AudioTrackIndex:      session.AudioTrackIndex,
		TranscodeAudio:       session.TranscodeAudio,
		TargetResolution:     session.TargetResolution,
		TargetVideoCodec:     session.TargetVideoCodec,
		TargetAudioCodec:     session.TargetAudioCodec,
		TranscodeHWAccel:     session.TranscodeHWAccel,
	}
	if session.StreamBitrateKbps > 0 {
		v := session.StreamBitrateKbps
		row.StreamBitrateKbps = &v
	}
	if session.TargetBitrateKbps > 0 {
		v := session.TargetBitrateKbps
		row.TargetBitrateKbps = &v
	}
	return row
}
