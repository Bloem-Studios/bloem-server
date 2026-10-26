package playback

import (
	"context"
	"errors"
	"log/slog"
)

// refuseUnevaluatedReconstruct refuses Bloem authority denials, including
// wrapped denials tagged as provider failures. A genuine failure to evaluate
// limits is left to Silo's fail-open path.
func (m *TranscodeManager) refuseUnevaluatedReconstruct(ctx context.Context, sessionID string, userID int, method PlayMethod, err error) bool {
	if errors.Is(err, ErrPlaybackNotAllowed) || errors.Is(err, ErrTenantFrozen) || errors.Is(err, ErrTenantTranscodesExceeded) {
		slog.WarnContext(ctx, "playback session reconstruct refused by admission policy", "component", "playback",
			"session", sessionID, "playback_session_id", sessionID,
			"user", userID, "method", method, "error", err)
		return true
	}
	return false
}
