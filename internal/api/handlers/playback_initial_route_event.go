package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

// PlaybackRouteEventCommand is the typed v2 route report: the v3 event plus the
// client-minted identity that makes a retry after a lost 202 a no-op.
type PlaybackRouteEventCommand struct {
	EventID string
	Event   playback.RouteEventV3
}

// ReportInitialRouteEvent records one diagnostic event for an admitted attempt.
// Diagnostics never control playback: the caller must own the attempt and,
// when a session is named, that session; the event is queued and never waits
// on the store.
func (h *PlaybackHandler) ReportInitialRouteEvent(ctx context.Context, caller PlaybackCaller, command PlaybackRouteEventCommand) error {
	if err := h.validatePlaybackCaller(ctx, caller); err != nil {
		return err
	}
	if id, err := uuid.Parse(command.EventID); err != nil || id == uuid.Nil || id.String() != command.EventID {
		return playbackOperationError(http.StatusBadRequest, "bad_request", "A canonical event_id is required")
	}
	event := command.Event
	if !validRouteEventV3(event) {
		return playbackOperationError(http.StatusBadRequest, "bad_request", "Invalid route event")
	}
	if !h.allowRouteEventV3(caller.UserID, event.PlaybackAttemptID) {
		return playbackOperationError(http.StatusTooManyRequests, "event_rate_limited", "Playback route event rate exceeded")
	}
	var identity *playback.AttemptIdentityV3
	var err error
	if event.SessionID != "" {
		identity, err = h.PlanStoreV3.GetAttemptIdentity(ctx, event.SessionID)
	} else {
		identity, err = h.PlanStoreV3.GetAttemptIdentityByPlaybackAttemptID(ctx, event.PlaybackAttemptID)
	}
	if err != nil {
		if !errors.Is(err, playback.ErrSessionNotFound) {
			return playbackAuthorityOperationError()
		}
		return playbackOperationError(http.StatusForbidden, "forbidden", "Route event does not belong to this profile")
	}
	if identity.UserID != caller.UserID || identity.ProfileID != caller.ProfileID ||
		(event.SessionID != "" && identity.PlaybackAttemptID != event.PlaybackAttemptID) ||
		(identity.SessionID == "" && !terminalStartRouteEventV3(event)) {
		return playbackOperationError(http.StatusForbidden, "forbidden", "Route event does not belong to this profile")
	}
	event.Diagnostics = sanitizeDiagnosticsV3(event.Diagnostics)
	h.enqueueRouteEventV3(playback.RouteEventRecordV3{RouteEventV3: event, EventID: command.EventID, UserID: caller.UserID, ProfileID: caller.ProfileID, ClientName: caller.ClientName, ClientVersion: caller.ClientVersion, ClientBuild: caller.ClientBuild, ClientChannel: caller.ClientChannel, ClientModel: event.Diagnostics["device_model"]})
	return nil
}
