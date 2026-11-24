package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	evt "github.com/Silo-Server/silo-server/internal/events"
)

// BloemAdminLogsSocketV2 keeps Silo's administrator log stream socket (ticket,
// origin and subprotocol checks, administrator check, recheck loop) and
// carries the minting request's tenant authority into the public upgrade, as
// the other v2 sockets do. Without it the upgrade, which has no request
// tenant, cannot read the viewer's scope and every log stream is refused as
// authority_unavailable (503).
type BloemAdminLogsSocketV2 struct {
	*AdminLogsSocketV2
	authority *bloemSocketAuthority
}

// WrapBloemAdminLogsSocketV2 adds Bloem's tenant authority to a socket Silo's
// constructor built, so the router keeps Silo's construction line. The
// socket's own setters (SetPublicOrigin, SetOverlayOrigins) still apply.
func WrapBloemAdminLogsSocketV2(socket *AdminLogsSocketV2, sessions bloemSocketSessions, users access.UserRepository, tenants apimw.TenantResolver, profiles bloemSocketProfiles) *BloemAdminLogsSocketV2 {
	authority := &bloemSocketAuthority{sessions: sessions, users: users, tenants: tenants, profiles: profiles}
	if socket != nil && socket.Validate != nil {
		// A socket built without a validator stays unavailable (503).
		authority.base = socket.Validate
		socket.Validate = authority.validate
	}
	return &BloemAdminLogsSocketV2{AdminLogsSocketV2: socket, authority: authority}
}

// Available reports whether the handshake is served on this process.
func (h *BloemAdminLogsSocketV2) Available() bool {
	return h != nil && h.authority != nil && h.authority.base != nil && h.AdminLogsSocketV2.Available()
}

// Mint binds the request's tenant to the identity before Silo's mint
// validates it and stores it in the single-use ticket.
func (h *BloemAdminLogsSocketV2) Mint(ctx context.Context, identity evt.SocketIdentity) (string, error) {
	if !h.Available() {
		return h.AdminLogsSocketV2.Mint(ctx, identity)
	}
	bound, err := h.authority.bind(ctx, identity)
	if err != nil {
		return "", err
	}
	return h.AdminLogsSocketV2.Mint(ctx, bound)
}
