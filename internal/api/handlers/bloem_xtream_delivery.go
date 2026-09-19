package handlers

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/livetv"
)

// Called only after the ordinary native viewer, ownership and session-lease
// checks. The opaque reference is not itself authority or a network address.
func (h *LiveTVHandler) serveXtreamSession(w http.ResponseWriter, ctx context.Context, sessionID, reference string) bool {
	if !strings.HasPrefix(reference, "xtream:") {
		return false
	}
	body, err := h.service.OpenXtreamSessionSource(ctx, sessionID)
	if err != nil {
		writeLiveTVError(w, err)
		return true
	}
	if body == nil {
		writeLiveTVError(w, livetv.ErrNotFound)
		return true
	}
	defer func() { _ = body.Close() }()
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)
	// Network/lease failures end this response, never replay the uncertain
	// upstream request or disclose a provider URL through an error response.
	_, _ = io.Copy(w, body)
	return true
}
