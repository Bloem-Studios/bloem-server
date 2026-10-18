package handlers

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/redis/go-redis/v9"
)

// BloemPlaybackControlSocketV2 retains the shared session/profile/installation
// admission while carrying the original tenant authority into the public upgrade.
type BloemPlaybackControlSocketV2 struct {
	*PlaybackControlSocketV2
	authority *bloemSocketAuthority
}

func NewBloemPlaybackControlSocketV2(playbackHandler *PlaybackHandler, client *redis.Client, sessions bloemSocketSessions, users access.UserRepository, resolver apimw.ViewerResolver, actingAdmin apimw.ActingAdminDecider, tenants apimw.TenantResolver, profiles bloemSocketProfiles, publicURL string) *BloemPlaybackControlSocketV2 {
	socket := NewPlaybackControlSocketV2(playbackHandler, client, sessions, users, resolver, actingAdmin, publicURL)
	authority := &bloemSocketAuthority{sessions: sessions, users: users, tenants: tenants, profiles: profiles, base: socket.Validate}
	socket.Validate = authority.validate
	return &BloemPlaybackControlSocketV2{PlaybackControlSocketV2: socket, authority: authority}
}

func (h *BloemPlaybackControlSocketV2) Available() bool {
	return h != nil && h.PlaybackControlSocketV2 != nil && h.authority != nil &&
		h.authority.base != nil && h.authority.tenants != nil && h.authority.profiles != nil &&
		h.PlaybackControlSocketV2.Available()
}

func (h *BloemPlaybackControlSocketV2) Mint(ctx context.Context, identity evt.SocketIdentity, playbackSessionID, installationID string) (string, time.Time, error) {
	if !h.Available() {
		return "", time.Time{}, ErrPlaybackControlSocketUnavailable
	}
	bound, err := h.authority.bind(ctx, identity)
	if err != nil {
		return "", time.Time{}, err
	}
	return h.PlaybackControlSocketV2.Mint(ctx, bound, playbackSessionID, installationID)
}
