package playback

import (
	"context"
	"log/slog"
)

// StrictAdmissionFn (TranscodeManager) reports whether reconstruct admission
// fails CLOSED when the limit provider itself cannot be evaluated. Nil or
// false is upstream Silo's behavior: admit the session ungated rather than
// collapse a transient dependency error into a permanent 404. True is the
// stricter Bloem posture — never admit a session whose limits were never
// checked, accepting that a Postgres blip during a post-restart reconstruct
// wave stops playback for users who are in fact within their limits.
//
// This is a deliberate, operator-visible divergence
// (playback.strict_reconstruct_admission), not a silent fork of upstream's
// admission code. Keeping it as a runtime branch rather than an edit to the
// shared path is what stops it re-conflicting on every upstream merge.

// strictAdmission reports whether reconstruct admission should fail closed on an
// unevaluated limit provider. Defaults to false (upstream behavior) when the
// hook is unwired, so a manager built without settings access is never
// accidentally stricter than Silo.
func (m *TranscodeManager) strictAdmission() bool {
	if m.StrictAdmissionFn == nil {
		return false
	}
	return m.StrictAdmissionFn()
}

// refuseUnevaluatedReconstruct reports whether reconstructSession must refuse
// a session whose limit provider could not be evaluated. Only true when the
// operator has opted into the strict posture: a session whose limits were
// never evaluated is never admitted, even at the cost of refusing playback to
// users who are within their caps.
func (m *TranscodeManager) refuseUnevaluatedReconstruct(ctx context.Context, sessionID string, userID int, method PlayMethod, err error) bool {
	if !m.strictAdmission() {
		return false
	}
	slog.WarnContext(ctx, "playback session reconstruct refused: limits unevaluated and strict admission is enabled", "component", "playback",
		"session", sessionID, "playback_session_id", sessionID,
		"user", userID, "method", method, "error", err)
	return true
}
