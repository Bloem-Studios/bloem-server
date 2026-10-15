package api

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
)

// The app needs an account identity for boot; guests receive no contact or
// account-action details. Personal previews retain the ordinary account view.
type previewAccountService struct{ apiv2.AccountService }

func bloemPreviewAccounts(next apiv2.AccountService) apiv2.AccountService {
	if next == nil {
		return nil
	}
	return previewAccountService{AccountService: next}
}
func (p previewAccountService) CurrentUser(ctx context.Context, claims *auth.Claims) (handlers.UserView, error) {
	view, err := p.AccountService.CurrentUser(ctx, claims)
	if err != nil || claims == nil || claims.AuthMethod != auth.AuthMethodPreviewGuest {
		return view, err
	}
	return handlers.UserView{ID: view.ID, Username: "Preview", Role: models.RoleUser, Permissions: []string{}}, nil
}
