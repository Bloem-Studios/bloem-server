package api

import "context"

func (v bloemSessionValidator) ActiveSessionRole(ctx context.Context, id string) (string, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return "", valid, err
}
func (v seasonalMountSessions) ActiveSessionRole(ctx context.Context, id string) (string, bool, error) {
	valid, err := v.IsValid(ctx, id)
	return "", valid, err
}
