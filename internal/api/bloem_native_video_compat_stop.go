package api

import (
	"context"
	"log/slog"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// revokeStopped ends native delivery for a playback session a Jellyfin client
// stopped, or that expired: it writes the deny tombstone, removes the source
// binding and revokes a worker grant, as a native stop does. Sessions bound by
// another credential keep their own stop path.
func (c *NativeVideoCompat) revokeStopped(ctx context.Context, session *playback.Session) {
	if c == nil || session == nil || session.ID == "" {
		return
	}
	delivery := c.delivery.Load()
	if delivery == nil || delivery.service.Bindings == nil {
		return
	}
	binding, err := delivery.service.Bindings.Get(ctx, session.ID, session.MediaFileID)
	if err != nil || binding.Authority.TokenType != nativeVideoCompatTokenType {
		return
	}
	delivery.service.Deny.Deny(ctx, session.ID)
	if err := delivery.service.Bindings.RevokeSession(ctx, session.ID); err != nil {
		slog.WarnContext(ctx, "jellyfin native video binding revocation failed", "component", "native_video", "session", session.ID, "error", err)
	}
	if delivery.service.WorkerGrants != nil && delivery.service.WorkerGrants.Enabled() && session.TranscodeTransportID != "" {
		if err := delivery.service.WorkerGrants.RevokeTransport(ctx, session.TranscodeTransportID); err != nil {
			slog.WarnContext(ctx, "jellyfin native worker grant revocation failed", "component", "native_video", "session", session.ID, "error", err)
		}
	}
}

// watchStops revokes compat native delivery whenever a playback session
// finishes or expires.
func (c *NativeVideoCompat) watchStops(sessions *playback.SessionManager) {
	if c == nil || sessions == nil {
		return
	}
	sessions.AddFinishHook(func(ctx context.Context, session *playback.Session) {
		c.revokeStopped(context.WithoutCancel(ctx), session)
	})
	sessions.AddExpirationHook(func(session *playback.Session) {
		c.revokeStopped(context.Background(), session)
	})
}
