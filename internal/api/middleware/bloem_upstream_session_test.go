package middleware

import "context"

func (v fixedSessionValidator) ActiveSessionRole(context.Context, string) (string, bool, error) {
	return "user", v.valid, nil
}
func (v failingSessionValidator) ActiveSessionRole(ctx context.Context, id string) (string, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return "", valid, err
}
func (v audienceTicketSessionValidator) ActiveSessionRole(ctx context.Context, id string) (string, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return "", valid, err
}
func (v alwaysValidSession) ActiveSessionRole(ctx context.Context, id string) (string, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return "", valid, err
}
