package jellycompat

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/livetv"
)

// loadOpenLiveStream resolves the same opener on every API replica. Production
// uses the shared native-session ledger, never an in-process cache which could
// outlive a release or become inaccessible when the load balancer moves a call.
func (h *LiveTVHandler) loadOpenLiveStream(ctx context.Context, id string, caller *Session) (*openLiveStream, error) {
	if caller == nil {
		return nil, livetv.ErrNotFound
	}
	if !h.service.HasSharedCompatStreams() {
		h.mu.Lock()
		stream := h.streams[id]
		h.mu.Unlock()
		if stream == nil {
			return nil, livetv.ErrNotFound
		}
		if stream.OpenerToken != "" && stream.OpenerToken != caller.Token {
			return nil, errLiveTVForbidden
		}
		return stream, nil
	}
	shared, err := h.service.GetCompatStream(ctx, id, caller.Token)
	if err != nil {
		return nil, err
	}
	if _, err := h.service.GetSessionForViewer(ctx, shared.NativeSession, caller.StreamAppUserID, caller.ProfileID, true); err != nil {
		return nil, err
	}
	upstream, err := h.service.ResolveSessionUpstreamURL(ctx, shared.NativeSession)
	if err != nil {
		return nil, err
	}
	return &openLiveStream{ID: shared.ID, ChannelID: shared.ChannelID, NativeSession: shared.NativeSession, SourceURL: upstream, OpenedAt: shared.OpenedAt, OpenerToken: caller.Token}, nil
}
