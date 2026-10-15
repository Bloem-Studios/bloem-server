package middleware

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/auth"
)

func (v fixedSessionValidator) ActiveSession(context.Context, string) (auth.SessionAuthority, bool, error) {
	return auth.SessionAuthority{Role: "user"}, v.valid, nil
}
func (v failingSessionValidator) ActiveSession(ctx context.Context, id string) (auth.SessionAuthority, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return auth.SessionAuthority{}, valid, err
}
func (v audienceTicketSessionValidator) ActiveSession(ctx context.Context, id string) (auth.SessionAuthority, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return auth.SessionAuthority{}, valid, err
}
func (v alwaysValidSession) ActiveSession(ctx context.Context, id string) (auth.SessionAuthority, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return auth.SessionAuthority{}, valid, err
}
