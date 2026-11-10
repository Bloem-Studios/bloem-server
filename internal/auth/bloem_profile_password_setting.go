package auth

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/config"
)

// ErrProfilePasswordsDisabled refuses a correct profile password, or setting
// one, because the server turned profile passwords off. Like the server-wide
// password switch, it does not end direct-profile sessions already running,
// and clearing a stored profile password stays allowed.
var ErrProfilePasswordsDisabled = errors.New("profile passwords are turned off on this server")

// ProfilePasswordsEnabled reports whether household profiles may sign in
// with, and be given, their own password. The setting defaults off, and a
// read error counts as off.
func (s *ProfileCredentialService) ProfilePasswordsEnabled(ctx context.Context) bool {
	if s == nil || s.pool == nil {
		return false
	}
	enabled, err := profilePasswordsEnabled(ctx, s.pool)
	return err == nil && enabled
}

func profilePasswordsEnabled(ctx context.Context, db rowQuerier) (bool, error) {
	return readBoolSetting(ctx, db, config.ProfilePasswordsEnabledSettingKey, false)
}

func ensureProfilePasswordsEnabled(ctx context.Context, db rowQuerier) error {
	enabled, err := profilePasswordsEnabled(ctx, db)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrProfilePasswordsDisabled
	}
	return nil
}
