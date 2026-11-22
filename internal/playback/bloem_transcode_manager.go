package playback

import (
	"context"
	"errors"
	"log/slog"
)

// refuseUnevaluatedReconstruct refuses Bloem authority denials, including
// wrapped denials tagged as provider failures, and a pooled household's
// session that could not reserve (ErrHouseholdReservationUnavailable). Any
// other failure is left to Silo's fail-open path, which registers s without a
// fleet reservation; s then owes one, which the cleanup pass makes once its
// limits can be read again if its account plays in a pooled household
// (reserveOwedSessions).
func (m *TranscodeManager) refuseUnevaluatedReconstruct(ctx context.Context, s *Session, err error) bool {
	if errors.Is(err, ErrPlaybackNotAllowed) || errors.Is(err, ErrTenantFrozen) || errors.Is(err, ErrTenantTranscodesExceeded) || errors.Is(err, ErrHouseholdReservationUnavailable) {
		slog.WarnContext(ctx, "playback session reconstruct refused by admission policy", "component", "playback",
			"session", s.ID, "playback_session_id", s.ID,
			"user", s.UserID, "method", s.PlayMethod, "error", err)
		return true
	}
	s.reservationOwed = true
	return false
}
