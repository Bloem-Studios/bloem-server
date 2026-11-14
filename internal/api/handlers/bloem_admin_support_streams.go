package handlers

// Bloem-owned. The organization's live streams for its administrators: who is
// watching, what, on which device, and whether it plays direct or transcoded.
// No IP address, node, routing or bitrate: those describe the server, not the
// organization. Stop and message call Silo's own administrator services with
// the organization filter applied first, so an id outside the organization is
// a 404 before any command is issued.

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/playback"
)

const (
	streamsDefaultLimit = 50
	streamsMaxLimit     = 100
	// streamAuditMessageLimit cuts the message text kept in the audit event.
	streamAuditMessageLimit = 256
)

// SupportStreamSessions reads live sessions with an organization filter.
// *AdminHandler satisfies it.
type SupportStreamSessions interface {
	ReadAdminPlaybackSessions(ctx context.Context, query PlaybackSessionsQuery, after string, limit int) ([]AdminPlaybackSessionView, error)
}

// SupportStreamControl is Silo's administrator terminate and sequenced
// command service. *AdminPlaybackControlHandler satisfies it.
type SupportStreamControl interface {
	AdminTerminateAvailable() bool
	Terminate(ctx context.Context, in AdminTerminateInput) (AdminTerminateView, error)
	AdminPlaybackCommandsAvailable() bool
	Command(ctx context.Context, in AdminPlaybackCommandInput) (AdminPlaybackCommandView, error)
}

// SetStreams wires the stream routes. Both may be nil; the routes then answer
// capability false and 503.
func (h *BloemOrganizationSupportHandler) SetStreams(sessions SupportStreamSessions, control SupportStreamControl) {
	if h != nil {
		h.sessions, h.control = sessions, control
	}
}

// Capabilities reports which stream operations this server can serve.
func (h *BloemOrganizationSupportHandler) streamCapabilities() (list, stop, message bool) {
	if h == nil {
		return false, false, false
	}
	list = h.sessions != nil
	stop = h.control != nil && h.control.AdminTerminateAvailable()
	message = h.control != nil && h.control.AdminPlaybackCommandsAvailable()
	return list, stop, message
}

type supportStreamDevice struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
	ID       string `json:"id"`
	App      string `json:"app"`
}

// SupportStream is one live stream of the organization as support sees it.
type SupportStream struct {
	SessionID       string              `json:"session_id"`
	AccountID       int                 `json:"account_id"`
	PersonName      string              `json:"person_name"`
	ProfileID       string              `json:"profile_id"`
	ProfileName     string              `json:"profile_name"`
	Title           string              `json:"title"`
	SeriesName      string              `json:"series_name,omitempty"`
	SeasonNumber    *int                `json:"season_number,omitempty"`
	EpisodeNumber   *int                `json:"episode_number,omitempty"`
	MediaType       string              `json:"media_type"`
	StartedAt       time.Time           `json:"started_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	PositionSeconds float64             `json:"position_seconds"`
	DurationSeconds *int                `json:"duration_seconds"`
	IsPaused        bool                `json:"is_paused"`
	PlayMethod      string              `json:"play_method"`
	VideoDecision   string              `json:"video_decision"`
	AudioDecision   string              `json:"audio_decision"`
	Device          supportStreamDevice `json:"device"`
	CanStop         bool                `json:"can_stop"`
	CanMessage      bool                `json:"can_message"`
}

func supportStreamFrom(row AdminPlaybackSessionView, deviceName string, protected bool, canStop, canMessage bool) SupportStream {
	title := row.MediaTitle
	if row.SeriesName != "" && row.EpisodeName != "" {
		title = row.EpisodeName
	}
	method := row.EffectivePlayMethod
	if method == "audio" {
		method = "direct_stream"
	}
	if method == "" {
		method = "direct"
	}
	name := deviceName
	if name == "" {
		name = row.ClientName
	}
	return SupportStream{
		SessionID: row.SessionID, AccountID: row.UserID, PersonName: row.Username, ProfileID: row.ProfileID, ProfileName: row.ProfileName,
		Title: title, SeriesName: row.SeriesName, SeasonNumber: row.SeasonNumber, EpisodeNumber: row.EpisodeNumber, MediaType: row.MediaType,
		StartedAt: row.StartedAt, UpdatedAt: row.UpdatedAt, PositionSeconds: row.PositionSeconds, DurationSeconds: row.FileDuration, IsPaused: row.IsPaused,
		PlayMethod: method, VideoDecision: row.VideoDecision, AudioDecision: row.AudioDecision,
		Device:  supportStreamDevice{Name: name, Platform: row.DevicePlatform, ID: row.DeviceID, App: row.ClientLabel},
		CanStop: canStop && !protected, CanMessage: canMessage && !protected,
	}
}

// deviceNames reads the registered name of each (account, device) pair.
func (h *BloemOrganizationSupportHandler) deviceNames(ctx context.Context, rows []AdminPlaybackSessionView) map[string]string {
	names := map[string]string{}
	var users []int
	var devices []string
	for _, row := range rows {
		if row.DeviceID != "" {
			users = append(users, row.UserID)
			devices = append(devices, row.DeviceID)
		}
	}
	if len(users) == 0 {
		return names
	}
	found, err := h.pool.Query(ctx, `
		SELECT DISTINCT ON (d.user_id, d.device_id) d.user_id, d.device_id, COALESCE(NULLIF(d.custom_name, ''), d.device_name)
		FROM user_devices d
		JOIN unnest($1::int[], $2::text[]) AS wanted(user_id, device_id) ON wanted.user_id = d.user_id AND wanted.device_id = d.device_id
		ORDER BY d.user_id, d.device_id, d.last_seen_at DESC`, users, devices)
	if err != nil {
		slog.WarnContext(ctx, "stream device names unavailable", "component", "api", "error", err)
		return names
	}
	defer found.Close()
	for found.Next() {
		var user int
		var device, name string
		if err := found.Scan(&user, &device, &name); err == nil && name != "" {
			names[strconv.Itoa(user)+"/"+device] = name
		}
	}
	if err := found.Err(); err != nil {
		slog.WarnContext(ctx, "stream device names incomplete", "component", "api", "error", err)
	}
	return names
}

func encodeStreamCursor(sessionID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sessionID))
}

func decodeStreamCursor(cursor string) (string, bool) {
	if cursor == "" {
		return "", true
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) == 0 || len(raw) > 256 {
		return "", false
	}
	return string(raw), true
}

// HandleListStreams handles GET /admin/organization/streams.
func (h *BloemOrganizationSupportHandler) HandleListStreams(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	for key := range query {
		if key != "limit" && key != "cursor" {
			writeAdminValidation(w, map[string]string{key: "is not a supported parameter"})
			return
		}
	}
	limit := streamsDefaultLimit
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > streamsMaxLimit {
			writeAdminValidation(w, map[string]string{"limit": "must be between 1 and 100"})
			return
		}
		limit = parsed
	}
	after, ok := decodeStreamCursor(query.Get("cursor"))
	if !ok {
		writeAdminValidation(w, map[string]string{"cursor": "is invalid"})
		return
	}
	if h.sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}
	organizationID := request.Tenant.OrganizationID
	actor, err := adminpeople.ReadActorFacts(r.Context(), h.pool, organizationID, request.Actor)
	if err != nil {
		h.writeSupportError(w, r, err, organizationID, 0)
		return
	}
	rows, err := h.sessions.ReadAdminPlaybackSessions(r.Context(), PlaybackSessionsQuery{OrganizationID: organizationID}, after, limit)
	if err != nil {
		slog.ErrorContext(r.Context(), "organization streams could not be read", "component", "api", "error", err)
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}
	nextCursor := ""
	if len(rows) > limit {
		rows = rows[:limit]
		nextCursor = encodeStreamCursor(rows[len(rows)-1].SessionID)
	}
	owners := make([]int, 0, len(rows))
	for _, row := range rows {
		owners = append(owners, row.UserID)
	}
	targets, err := adminpeople.ReadStreamTargets(r.Context(), h.pool, owners)
	if err != nil {
		h.writeSupportError(w, r, err, organizationID, 0)
		return
	}
	_, canStop, canMessage := h.streamCapabilities()
	names := h.deviceNames(r.Context(), rows)
	streams := make([]SupportStream, 0, len(rows))
	for _, row := range rows {
		protected := adminpeople.JudgeStream(actor, targets[row.UserID]) != nil
		streams = append(streams, supportStreamFrom(row, names[strconv.Itoa(row.UserID)+"/"+row.DeviceID], protected, canStop, canMessage))
	}
	writeJSON(w, http.StatusOK, struct {
		Streams    []SupportStream `json:"streams"`
		NextCursor string          `json:"next_cursor,omitempty"`
	}{streams, nextCursor})
}

// streamTarget loads the session through the organization filter and judges
// the stream rules. A session outside the organization is errSupportNotFound.
func (h *BloemOrganizationSupportHandler) streamTarget(ctx context.Context, request supportRequest, sessionID string) (AdminPlaybackSessionView, error) {
	if h.sessions == nil || h.control == nil {
		return AdminPlaybackSessionView{}, errSupportUnavailable
	}
	rows, err := h.sessions.ReadAdminPlaybackSessions(ctx, PlaybackSessionsQuery{OrganizationID: request.Tenant.OrganizationID, SessionID: sessionID}, "", 1)
	if err != nil {
		return AdminPlaybackSessionView{}, err
	}
	if len(rows) == 0 || rows[0].SessionID != sessionID {
		return AdminPlaybackSessionView{}, errSupportNotFound
	}
	return rows[0], nil
}

var errSupportUnavailable = errors.New("stream control is unavailable")

// auditStream records a stream event in its own transaction. The command has
// already been accepted, so a failure here is logged, not returned.
func (h *BloemOrganizationSupportHandler) auditStream(ctx context.Context, request supportRequest, row AdminPlaybackSessionView, action, outcome string, after map[string]any) {
	ctx = context.WithoutCancel(ctx)
	_, err := adminpeople.RunSupportTx(ctx, h.pool.Begin, func(tx pgx.Tx) (struct{}, error) {
		return struct{}{}, adminpeople.RecordAudit(ctx, tx, adminpeople.AuditEvent{
			Actor: request.Actor, Action: action, TargetType: "stream", TargetID: row.SessionID,
			OrganizationID: request.Tenant.OrganizationID, SubjectAccountID: row.UserID, Outcome: outcome, After: after,
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "support stream audit event was not recorded", "component", "api", "action", action, "error", err)
	}
}

// judgeStream refuses the actor's own stream and a platform account's, with an
// audited conflict row.
func (h *BloemOrganizationSupportHandler) judgeStream(ctx context.Context, request supportRequest, row AdminPlaybackSessionView, refusedAction string) error {
	actor, err := adminpeople.ReadActorFacts(ctx, h.pool, request.Tenant.OrganizationID, request.Actor)
	if err != nil {
		return err
	}
	targets, err := adminpeople.ReadStreamTargets(ctx, h.pool, []int{row.UserID})
	if err != nil {
		return err
	}
	if err := adminpeople.JudgeStream(actor, targets[row.UserID]); err != nil {
		var protected *adminpeople.TargetProtectedError
		if errors.As(err, &protected) {
			h.auditStream(ctx, request, row, refusedAction, "conflict", map[string]any{"reason": string(protected.Reason)})
		}
		return err
	}
	return nil
}

// checkStreamBudget counts the stream budget without a lock: stop and message
// are commands, not rows, so the check and the audit write are not one
// transaction. A burst can slightly overshoot the count; it cannot go on.
func (h *BloemOrganizationSupportHandler) checkStreamBudget(ctx context.Context, request supportRequest, sessionID string) error {
	return adminpeople.CheckBudget(ctx, h.pool, adminpeople.BudgetStream, request.Tenant.OrganizationID, request.Actor.AccountID, sessionID)
}

func (h *BloemOrganizationSupportHandler) writeStreamError(w http.ResponseWriter, r *http.Request, err error, organizationID uuid.UUID) {
	var stale *AdminPlaybackCommandStaleError
	switch {
	// NoReply wraps Unavailable, so it must be matched first: the session's
	// authority is revoked even when the holder did not answer.
	case errors.Is(err, ErrAdminTerminateNoReply), errors.Is(err, ErrAdminPlaybackCommandNoReply):
		supportDependencyUnavailable(w)
	case errors.Is(err, errSupportUnavailable):
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Stream control is unavailable")
	case errors.Is(err, ErrAdminTerminateUnavailable), errors.Is(err, ErrAdminPlaybackCommandUnavailable):
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Stream control is unavailable")
	case errors.Is(err, playback.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
	case errors.As(err, &stale):
		w.Header().Set(LatestSequenceHeader, strconv.FormatInt(stale.Latest, 10))
		writeError(w, http.StatusConflict, "stale_sequence", "The command sequence is behind the session's latest applied command; allocate a newer one")
	case errors.Is(err, ErrAdminPlaybackCommandStale):
		writeError(w, http.StatusConflict, "stale_sequence", "The command sequence is behind the session's latest applied command; allocate a newer one")
	case errors.Is(err, ErrAdminPlaybackCommandConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", "This command identity was already applied with different content")
	case errors.Is(err, ErrAdminPlaybackRealtimeRequired):
		writeError(w, http.StatusConflict, "realtime_unavailable", "The player has no realtime connection for this command")
	case errors.Is(err, ErrAdminPlaybackCommandInvalid):
		writeAdminValidation(w, map[string]string{"request": "the command identity or content is invalid"})
	default:
		h.writeSupportError(w, r, err, organizationID, 0)
	}
}

// LatestSequenceHeader is Silo's header on a stale command refusal.
const LatestSequenceHeader = "X-Silo-Latest-Sequence"

// HandleStopStream handles POST /admin/organization/streams/{session_id}/stop.
// It has Silo's terminate semantics: the session's authority is revoked
// durably first, then the player is told as best effort.
func (h *BloemOrganizationSupportHandler) HandleStopStream(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	if sessionID == "" || len(sessionID) > 128 {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if !decodeAdminPlatformJSON(w, r, &body) {
			return
		}
	}
	if utf8.RuneCountInString(body.Reason) > 1024 {
		writeAdminValidation(w, map[string]string{"reason": "must be at most 1024 characters"})
		return
	}
	organizationID := request.Tenant.OrganizationID
	row, err := h.streamTarget(r.Context(), request, sessionID)
	if err != nil {
		h.writeStreamError(w, r, err, organizationID)
		return
	}
	if err := h.checkStreamBudget(r.Context(), request, sessionID); err != nil {
		h.writeSupportError(w, r, err, organizationID, 0)
		return
	}
	if err := h.judgeStream(r.Context(), request, row, "stream.stop_refused"); err != nil {
		h.writeStreamError(w, r, err, organizationID)
		return
	}
	view, err := h.control.Terminate(r.Context(), AdminTerminateInput{SessionID: sessionID, ActorID: request.Actor.AccountID, Reason: body.Reason})
	if err != nil {
		if errors.Is(err, ErrAdminTerminateNoReply) {
			// Silo revokes the authority before it tries to tell the player.
			h.auditStream(r.Context(), request, row, "stream.stopped", "success", map[string]any{"authority_revoked": true, "client_notified": false})
		}
		h.writeStreamError(w, r, err, organizationID)
		return
	}
	h.auditStream(r.Context(), request, row, "stream.stopped", "success", map[string]any{"authority_revoked": view.AuthorityRevoked, "client_notified": view.ClientNotified})
	writeJSON(w, http.StatusOK, struct {
		SessionID        string `json:"session_id"`
		AuthorityRevoked bool   `json:"authority_revoked"`
		AlreadyRevoked   bool   `json:"already_revoked"`
		ClientNotified   bool   `json:"client_notified"`
	}{view.SessionID, view.AuthorityRevoked, view.AlreadyRevoked, view.ClientNotified})
}

// HandleMessageStream handles POST /admin/organization/streams/{session_id}/message.
// It keeps Silo's client-allocated command identity: command_id and a
// positive sequence, applied once per session.
func (h *BloemOrganizationSupportHandler) HandleMessageStream(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	if sessionID == "" || len(sessionID) > 128 {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	var body struct {
		CommandID string `json:"command_id"`
		Sequence  int64  `json:"sequence"`
		Title     string `json:"title"`
		Message   string `json:"message"`
	}
	if !decodeAdminPlatformJSON(w, r, &body) {
		return
	}
	fields := map[string]string{}
	if parsed, err := uuid.Parse(body.CommandID); err != nil || parsed.String() != strings.ToLower(body.CommandID) {
		fields["command_id"] = "must be a canonical UUID chosen by the client"
	}
	if body.Sequence < 1 || body.Sequence > AdminPlaybackCommandMaxSequence {
		fields["sequence"] = "must be a positive integer, at most 9007199254740991"
	}
	if n := utf8.RuneCountInString(body.Message); n < 1 || n > 2048 {
		fields["message"] = "must be 1 to 2048 characters"
	}
	if utf8.RuneCountInString(body.Title) > 256 {
		fields["title"] = "must be at most 256 characters"
	}
	if len(fields) > 0 {
		writeAdminValidation(w, fields)
		return
	}
	organizationID := request.Tenant.OrganizationID
	row, err := h.streamTarget(r.Context(), request, sessionID)
	if err != nil {
		h.writeStreamError(w, r, err, organizationID)
		return
	}
	if err := h.checkStreamBudget(r.Context(), request, sessionID); err != nil {
		h.writeSupportError(w, r, err, organizationID, 0)
		return
	}
	if err := h.judgeStream(r.Context(), request, row, "stream.message_refused"); err != nil {
		h.writeStreamError(w, r, err, organizationID)
		return
	}
	view, err := h.control.Command(r.Context(), AdminPlaybackCommandInput{
		SessionID: sessionID, CommandID: body.CommandID, Sequence: body.Sequence, Name: playback.CommandDisplayMessage,
		ActorID: request.Actor.AccountID, Title: body.Title, Message: body.Message,
	})
	if err != nil {
		h.writeStreamError(w, r, err, organizationID)
		return
	}
	status := http.StatusAccepted
	if view.Outcome == AdminPlaybackCommandReplayed {
		status = http.StatusOK
	} else {
		h.auditStream(r.Context(), request, row, "stream.message_sent", "success", map[string]any{"title": cutRunes(body.Title, streamAuditMessageLimit), "message": cutRunes(body.Message, streamAuditMessageLimit)})
	}
	writeJSON(w, status, struct {
		CommandID string `json:"command_id"`
		Sequence  int64  `json:"sequence"`
		Outcome   string `json:"outcome"`
		Delivery  string `json:"delivery"`
	}{view.CommandID, view.Sequence, view.Outcome, view.Delivery})
}

func cutRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}
