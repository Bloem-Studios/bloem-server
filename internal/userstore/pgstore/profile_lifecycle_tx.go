package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

var _ userstore.ProfileLifecycleTransactioner = (*PostgresUserStore)(nil)

func (s *PostgresUserStore) WithProfileLifecycleTransaction(ctx context.Context, tx pgx.Tx, fn func(userstore.ProfileLifecycleWriter) error) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1, $2)", preferenceSettingsAdvisoryClass, int32(s.userID)); err != nil {
		return fmt.Errorf("locking profile lifecycle transaction: %w", err)
	}
	return fn(&preferenceSettingsTx{exec: tx, userID: s.userID})
}

func (tx *preferenceSettingsTx) GetProfile(ctx context.Context, id string) (*userstore.Profile, error) {
	p, err := scanProfile(tx.exec.QueryRow(ctx, `
		SELECT id, name, avatar, pin_hash, COALESCE(login_email, ''), credential_revision, is_child, is_primary, max_content_rating,
		       quality_preference, language, preferred_metadata_language, subtitle_language, subtitle_mode,
		       auto_skip_intro, auto_skip_credits, auto_skip_recap, auto_play_next_preview, library_restrictions_enabled,
		       show_forced_subtitles, max_playback_quality, organization_id::text, access_group_id, created_at, updated_at
		FROM user_profiles WHERE user_id = $1 AND id = $2`, tx.userID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying profile %s: %w", id, err)
	}
	p.AllowedLibraryIDs, err = listProfileAllowedLibraries(ctx, tx.exec, tx.userID, id)
	return p, err
}

func (tx *preferenceSettingsTx) DeleteProfile(ctx context.Context, id string) error {
	return deleteProfile(ctx, tx.exec, tx.userID, id)
}
