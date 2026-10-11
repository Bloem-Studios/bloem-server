package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// Remote native predecessors require stop/start until shared authority can
// atomically retire an old transport while publishing its successor. Callers
// repeat this check under their preparation lifecycle lock before media work.
func (h *PlaybackHandler) nativeVideoPredecessorAdmissionV3(ctx context.Context, session *playback.Session, result playback.PlannerResultV3) *transportErrorV3 {
	if h.NativeVideo == nil {
		return nil
	}
	check := func(predecessor *playback.Session) *transportErrorV3 {
		if predecessor.TranscodeNodeURL == "" {
			return nil
		}
		native, err := h.nativeVideoPredecessorAuthorityV3(ctx, predecessor)
		if err != nil {
			return &transportErrorV3{reason: routeCapabilityUnavailableReasonV3, message: "Previous native playback authority could not be verified; retry before replacing this route.", retryable: true, cause: err}
		}
		if native {
			return &transportErrorV3{reason: routeCapabilityUnavailableReasonV3, message: "Replanning remote native playback requires stopping this session and a fresh playback start.", retryable: true}
		}
		return nil
	}
	if failure := check(session); failure != nil {
		return failure
	}
	if result.Plan == nil || result.Plan.SessionID == "" {
		return nil
	}
	if result.Plan.SessionID != session.ID {
		return &transportErrorV3{reason: "internal_error", message: "The playback plan does not belong to the session being prepared."}
	}
	current, err := h.sessionMgr.GetSession(session.ID)
	if err != nil {
		return &transportErrorV3{reason: "session_expired", message: "The playback session ended before its route could be prepared.", retryable: true, cause: err}
	}
	return check(current)
}

// Retained native authority belongs to the session, independently of the new
// target file or a changed catalog path. Before the first Bind completes, the
// predecessor's catalog file supplies the conservative fallback.
func (h *PlaybackHandler) nativeVideoPredecessorAuthorityV3(ctx context.Context, session *playback.Session) (bool, error) {
	if history, ok := h.NativeVideo.Bindings.(nativeVideoBindingHistory); ok {
		native, err := history.HasSession(ctx, session.ID)
		if err != nil || native {
			return native, err
		}
	}
	if h.NativeVideo.Files == nil {
		return false, nativeVideoUnavailable()
	}
	file, err := h.NativeVideo.Files.GetByID(ctx, session.MediaFileID)
	if err != nil {
		return false, err
	}
	if file == nil {
		return false, nativeVideoUnavailable()
	}
	return storagesource.IsNativeLocation(file.FilePath), nil
}

func nativeVideoProxyAdmissionErrorV3() *transportErrorV3 {
	return &transportErrorV3{reason: routeCapabilityUnavailableReasonV3, message: "Native video worker playback requires API relay; proxy source authority is unavailable.", retryable: true}
}
