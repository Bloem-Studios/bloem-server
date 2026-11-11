package handlers

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remote"
)

// Remote control is dormant (docs/architecture/admin-remote-control.md): its
// routes lived only on Silo's /api/v1, which Silo retired (Silo #3200), and
// nothing wires a RemoteObserver, so these hooks are inert until native
// remote-control routes are built.

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
// overrides. The
// durable attempt record is never rewritten: an admin replan (S-5a) narrows
// the request the planner sees while the record keeps the client's own
// request. executeReplanV3 reads it once per replan; the pin lives in the
// remote command store.
func (h *PlaybackHandler) applyRemotePlanOverridesV3(ctx context.Context, sessionID string, start playback.StartRequestV3) playback.StartRequestV3 {
	overrides, ok := h.remotePlanOverridesV3(ctx, sessionID)
	if !ok {
		return start
	}
	return playback.ApplyPlanOverridesV3(start, overrides)
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
