package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	evt "github.com/Silo-Server/silo-server/internal/events"
)

// BloemEventsSocketV2 keeps the shared protocol, ticket store, PIN verification,
// access fingerprint and revocation loop while restoring Bloem authority.
type BloemEventsSocketV2 struct {
	*EventsSocketV2
	*bloemSocketAuthority
}

func NewBloemEventsSocketV2(events *EventsHandler, tickets *evt.SocketTicketStore, sessions bloemSocketSessions, users access.UserRepository, resolver apimw.ViewerResolver, primary apimw.PrimaryProfileChecker, tenants apimw.TenantResolver, profiles bloemSocketProfiles, publicURL string) *BloemEventsSocketV2 {
	socket := NewEventsSocketV2(events, tickets, sessions, users, resolver, primary, publicURL)
	h := &BloemEventsSocketV2{EventsSocketV2: socket, bloemSocketAuthority: &bloemSocketAuthority{sessions: sessions, users: users, tenants: tenants, profiles: profiles, base: socket.Validate}}
	socket.Validate = h.validate
	return h
}

func (h *BloemEventsSocketV2) Mint(ctx context.Context, identity evt.SocketIdentity) (string, error) {
	if h == nil || h.EventsSocketV2 == nil {
		return "", evt.ErrSocketTicket
	}
	bound, err := h.bind(ctx, identity)
	if err != nil {
		return "", err
	}
	return h.EventsSocketV2.Mint(ctx, bound)
}
