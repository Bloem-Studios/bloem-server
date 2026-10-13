package handlers

import (
	"context"
	"os"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/themesongs"
)

// BloemContentAccessFilter carries transport restrictions without changing the
// catalog predicates. A denied player can still discover items and themes.
func BloemContentAccessFilter(ctx context.Context, filter catalog.AccessFilter) catalog.AccessFilter {
	if scope, ok := access.GetScope(ctx); ok {
		filter.PlaybackDenied = filter.PlaybackDenied || !scope.PlaybackAllowed
		filter.DownloadDenied = filter.DownloadDenied || !scope.DownloadAllowed
	}
	return filter
}

// BloemThemeSongsHandler retains the host's file, session and profile checks
// and adds Bloem's playback policy at mint and local grant-consumption time.
type BloemThemeSongsHandler struct{ *ThemeSongsHandler }

func (h *BloemThemeSongsHandler) Authorize(ctx context.Context, identity themesongs.Identity, owner, id string, filter catalog.AccessFilter, accepted []themesongs.Format, expiry time.Time) (themesongs.Authorization, error) {
	filter = BloemContentAccessFilter(ctx, filter)
	if filter.PlaybackDenied {
		return themesongs.Authorization{}, themesongs.ErrGrant
	}
	return h.ThemeSongsHandler.Authorize(ctx, identity, owner, id, filter, accepted, expiry)
}
func (h *BloemThemeSongsHandler) OpenGrant(ctx context.Context, owner, id, token string) (themesongs.File, themesongs.Delivery, *os.File, error) {
	// Wrap the delegate's fresh resolver so policy is checked in the same
	// resolution as profile/file authority, after session/account validation.
	delegate := *h.ThemeSongsHandler
	delegate.Resolver = bloemThemePlaybackResolver{delegate.Resolver}
	return delegate.OpenGrant(ctx, owner, id, token)
}

type bloemThemePlaybackResolver struct{ apimw.ViewerResolver }

func (r bloemThemePlaybackResolver) Resolve(ctx context.Context, in access.ResolveInput) (access.Scope, error) {
	if r.ViewerResolver == nil {
		return access.Scope{}, themesongs.ErrUnavailable
	}
	scope, err := r.ViewerResolver.Resolve(ctx, in)
	if err == nil && !scope.PlaybackAllowed {
		return access.Scope{}, themesongs.ErrGrant
	}
	return scope, err
}
