package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	evt "github.com/Silo-Server/silo-server/internal/events"
)

// BloemWatchTogetherSocketV2 keeps Silo's room socket (room proof, single-use
// ticket, origin and subprotocol checks, room admission and the recheck loop)
// and carries the minting request's tenant authority into the public upgrade,
// as the events and playback control sockets do. Without it the upgrade, which
// has no request tenant, cannot read the viewer's scope and every room
// connection is refused as authority_unavailable (503).
type BloemWatchTogetherSocketV2 struct {
	*WatchTogetherSocketV2
	authority *bloemSocketAuthority
}

// WrapBloemWatchTogetherSocketV2 adds Bloem's tenant authority to a socket
// Silo's constructor built, so the router keeps Silo's construction line. The
// socket's own setters (SetPublicOrigin, SetOverlayOrigins) still apply.
func WrapBloemWatchTogetherSocketV2(socket *WatchTogetherSocketV2, sessions bloemSocketSessions, users access.UserRepository, tenants apimw.TenantResolver, profiles bloemSocketProfiles) *BloemWatchTogetherSocketV2 {
	authority := &bloemSocketAuthority{sessions: sessions, users: users, tenants: tenants, profiles: profiles}
	if socket != nil && socket.Validate != nil {
		// A socket built without a validator stays unavailable (503).
		authority.base = socket.Validate
		socket.Validate = authority.validate
	}
	return &BloemWatchTogetherSocketV2{WatchTogetherSocketV2: socket, authority: authority}
}

// MintRoomSocket binds the request's tenant to the identity before Silo's
// mint validates it and stores it in the single-use room credential.
func (h *BloemWatchTogetherSocketV2) MintRoomSocket(ctx context.Context, roomID, proof string, identity evt.SocketIdentity) (string, time.Time, error) {
	if h == nil || h.authority == nil || h.authority.base == nil || !h.available() {
		return "", time.Time{}, apiError(503, "unavailable", "Room socket is unavailable")
	}
	bound, err := h.authority.bind(ctx, identity)
	if errors.Is(err, evt.ErrSocketAuthorityUnavailable) {
		return "", time.Time{}, err
	}
	if err != nil {
		return "", time.Time{}, apiError(403, "forbidden", "Current room authority is required")
	}
	return h.WatchTogetherSocketV2.MintRoomSocket(ctx, roomID, proof, bound)
}
