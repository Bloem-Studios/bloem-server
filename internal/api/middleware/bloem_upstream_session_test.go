package middleware

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/auth"
)

type fixedSessionValidator struct{ valid bool }

func (v fixedSessionValidator) IsValid(context.Context, string) (bool, error) { return v.valid, nil }

func (v fixedSessionValidator) ActiveSession(context.Context, string, int) (auth.SessionAuthority, bool, error) {
	return auth.SessionAuthority{Role: "user"}, v.valid, nil
}
func (v failingSessionValidator) ActiveSession(ctx context.Context, id string, _ int) (auth.SessionAuthority, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return auth.SessionAuthority{}, valid, err
}
func (v alwaysValidSession) ActiveSession(ctx context.Context, id string, _ int) (auth.SessionAuthority, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return auth.SessionAuthority{}, valid, err
}
