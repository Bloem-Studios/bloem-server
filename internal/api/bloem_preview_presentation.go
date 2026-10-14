package api

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/preview"
)

type bloemPreviewPresentation struct {
	pool   *pgxpool.Pool
	viewer apimw.ViewerResolver
}

func (p bloemPreviewPresentation) AccountPresentation(ctx context.Context, claims *auth.Claims) (*preview.Presentation, bool, error) {
	scope, ok := access.GetScope(ctx)
	if !ok {
		var err error
		scope, err = p.viewer.Resolve(ctx, access.ResolveInput{UserID: claims.UserID, SessionID: claims.SessionID})
		if err != nil {
			return nil, false, err
		}
	}
	if !apimw.IsPreviewPrincipal(ctx) {
		return nil, scope.PlaybackAllowed, nil
	}
	view, err := preview.NewStore(p.pool).Presentation(ctx, claims.UserID, claims.SessionID, apimw.IsPreviewGuest(ctx))
	return view, false, err
}
