package api

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/auth"
)

func (v bloemSessionValidator) ActiveSession(ctx context.Context, id string) (auth.SessionAuthority, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return auth.SessionAuthority{}, valid, err
}
func (v seasonalMountSessions) ActiveSession(ctx context.Context, id string) (auth.SessionAuthority, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return auth.SessionAuthority{}, valid, err
}
