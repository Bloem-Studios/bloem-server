package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remote"
)

// upstreamHelloCommands drops the remote-control-only names from a hello's
// command list so the upstream validator sees only its own vocabulary.
func upstreamHelloCommands(commands []playback.CommandName) []playback.CommandName {
	kept := make([]playback.CommandName, 0, len(commands))
	for _, name := range commands {
		if remote.IsRemoteOnlyCommand(name) {
			continue
		}
		kept = append(kept, name)
	}
	return kept
}

// bloemObserveHello mirrors a session hello into the remote control audit.
func (h *PlaybackHandler) bloemObserveHello(sessionID string, commands []playback.CommandName) {
	if h.RemoteObserver == nil {
		return
	}
	if session, err := h.sessionMgr.GetSession(sessionID); err == nil && session != nil {
		h.RemoteObserver.OnHello(context.Background(), remoteSessionInfo(session), commands)
	}
}

// bloemObserveAck mirrors a command ack into the remote control audit. Only
// the session the command was sent to may move it to accepted.
func (h *PlaybackHandler) bloemObserveAck(sessionID, commandID string) {
	if h.RemoteObserver == nil {
		return
	}
	if record, ok := h.getRealtimeCommand(commandID); ok && record.SessionID == sessionID {
		h.RemoteObserver.OnAck(context.Background(), commandID)
	}
}

// bloemObserveResult mirrors a command result into the remote control audit.
func (h *PlaybackHandler) bloemObserveResult(result playback.ResultEnvelope) {
	if h.RemoteObserver == nil {
		return
	}
	h.RemoteObserver.OnResult(context.Background(), result.CommandID, result.Status == playback.RealtimeResultStatusCompleted, result.Error)
}
