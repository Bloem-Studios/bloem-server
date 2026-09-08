package handlers

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/Silo-Server/silo-server/internal/playback"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// Log only fixed classifications. Error strings can contain signed URLs,
// credentials, remote response bodies, or media paths; retain those in the
// transport error, never in this diagnostic record.
func logInitialAbortV3(ctx context.Context, stage, attemptID, sessionID string, cause error) {
	attrs := []any{
		"component", "api", "stage", stage, "reason", initialAbortReasonV3(cause),
		"request_id", chimw.GetReqID(ctx), "playback_attempt_id", attemptID,
		"playback_session_id", sessionID}
	if prepared, ok := errors.AsType[*executorPreparationErrorV3](cause); ok {
		attrs = append(attrs, "preparation_class", prepared.class, "preparation_status", prepared.status)
	}
	slog.WarnContext(ctx, "initial playback activation aborted", attrs...)
}

func initialAbortReasonV3(cause error) string {
	switch {
	case errors.Is(cause, context.Canceled):
		return "canceled"
	case errors.Is(cause, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(cause, os.ErrPermission):
		return "permission_denied"
	case errors.Is(cause, os.ErrNotExist):
		return "not_found"
	case errors.Is(cause, playback.ErrInitialActivationConflictV3):
		return "activation_conflict"
	case errors.Is(cause, playback.ErrInitialActivationInvalidV3):
		return "activation_invalid"
	case errors.Is(cause, playback.ErrInitialActivationUnavailableV3):
		return "activation_unavailable"
	default:
		return "dependency_failure"
	}
}
