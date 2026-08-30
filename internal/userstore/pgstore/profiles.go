package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// scanProfile scans a profile row, converting TIMESTAMPTZ to string.
func scanProfile(scanner interface {
	Scan(dest ...any) error
}) (*userstore.Profile, error) {
	var p userstore.Profile
	var createdAt, updatedAt time.Time
	err := scanner.Scan(
		&p.ID, &p.Name, &p.Avatar, &p.PINHash, &p.LoginEmail, &p.CredentialRevision, &p.IsChild, &p.IsPrimary, &p.MaxContentRating,
		&p.QualityPreference, &p.Language, &p.PreferredMetadataLanguage, &p.SubtitleLanguage, &p.SubtitleMode,
		&p.AutoSkipIntro, &p.AutoSkipCredits, &p.AutoSkipRecap, &p.AutoPlayNextPreview,
		&p.LibraryRestrictionsEnabled,
		&p.ShowForcedSubtitles, &p.MaxPlaybackQuality, &p.OrganizationID, &p.AccessGroupID, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	p.CreatedAt = timeToString(createdAt)
	p.UpdatedAt = timeToString(updatedAt)
	return &p, nil
}

func (s *PostgresUserStore) CreateProfile(ctx context.Context, p userstore.Profile) error {
	return createProfile(ctx, s.pool, s.userID, p)
}

// CreateProfileInTransaction inserts a profile using a caller-owned
// transaction. Account lifecycle creation uses it to bind the generated
// profile to the same receipt as the account and membership.
func (s *PostgresUserStore) CreateProfileInTransaction(ctx context.Context, tx pgx.Tx, p userstore.Profile) error {
	return createProfile(ctx, tx, s.userID, p)
}

func createProfile(
	ctx context.Context,
	exec preferenceSettingsExecutor,
	userID int,
	p userstore.Profile,
) error {
	if p.ID == "" {
		p.ID = generateUUID()
	}
	now := nowUTC()
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	if p.UpdatedAt == "" {
		p.UpdatedAt = now
	}
	if !p.ShowForcedSubtitles {
		p.ShowForcedSubtitles = true
	}

	// The first profile created for a user becomes the primary, giving it
	// rights to manage the household's other profiles without requiring a
	// server-wide admin role.
	var hasExisting bool
	if err := exec.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM user_profiles WHERE user_id = $1)", userID,
	).Scan(&hasExisting); err != nil {
		return fmt.Errorf("checking existing profiles for user %d: %w", userID, err)
	}
	if !hasExisting {
		p.IsPrimary = true
	} else {
		p.IsPrimary = false
	}

	if p.OrganizationID == "" {
		organizationID, legacyGroupID, err := tenancy.NewProfileIdentityResolver(exec).ResolveLegacyProfileIdentity(ctx, userID)
		if err != nil {
			return fmt.Errorf("resolving legacy identity for profile %s: %w", p.ID, err)
		}
		p.OrganizationID = organizationID.String()
		if p.AccessGroupID == nil {
			p.AccessGroupID = legacyGroupID
		}
	} else {
		var activeMembership bool
		if err := exec.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM organization_memberships memberships
				JOIN organizations ON organizations.id = memberships.organization_id
				WHERE memberships.account_id = $1
				  AND memberships.organization_id = $2
				  AND memberships.status = 'active'
				  AND organizations.status <> 'suspended'
			)`, userID, p.OrganizationID).Scan(&activeMembership); err != nil {
			return fmt.Errorf("validating organization for profile %s: %w", p.ID, err)
		}
		if !activeMembership {
			return fmt.Errorf("validating organization for profile %s: %w", p.ID, tenancy.ErrTenantNotFoundOrHidden)
		}
	}
	if p.AccessGroupID == nil {
		var defaultGroupID int64
		if err := exec.QueryRow(ctx, `
			SELECT id
			FROM access_groups
			WHERE organization_id = $1
			  AND is_default`, p.OrganizationID).Scan(&defaultGroupID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("resolving default access group for profile %s: %w", p.ID, tenancy.ErrTenantNotFoundOrHidden)
			}
			return fmt.Errorf("resolving default access group for profile %s: %w", p.ID, err)
		}
		p.AccessGroupID = &defaultGroupID
	}

	_, err := exec.Exec(ctx, `
		INSERT INTO user_profiles (
			id, user_id, name, avatar, pin_hash, is_child, is_primary, max_content_rating,
			quality_preference, language, preferred_metadata_language, subtitle_language, subtitle_mode,
			auto_skip_intro, auto_skip_credits, auto_skip_recap, auto_play_next_preview,
			library_restrictions_enabled,
			show_forced_subtitles, max_playback_quality, organization_id, access_group_id, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21,
			$22, $23, $24)`,
		p.ID, userID, p.Name, p.Avatar, p.PINHash, p.IsChild, p.IsPrimary, p.MaxContentRating,
		p.QualityPreference, p.Language, p.PreferredMetadataLanguage, p.SubtitleLanguage, p.SubtitleMode,
		p.AutoSkipIntro, p.AutoSkipCredits, p.AutoSkipRecap, p.AutoPlayNextPreview,
		p.LibraryRestrictionsEnabled,
		p.ShowForcedSubtitles, p.MaxPlaybackQuality, p.OrganizationID, p.AccessGroupID, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting profile %s: %w", p.ID, err)
	}
	if err := replaceProfileAllowedLibraries(ctx, exec, userID, p.ID, p.AllowedLibraryIDs); err != nil {
		return err
	}
	return nil
}

func (s *PostgresUserStore) GetProfile(ctx context.Context, id string) (*userstore.Profile, error) {
	return getProfile(ctx, s.pool, s.userID, id)
}

// GetProfileInTransaction reads a profile through a caller-owned transaction.
func (s *PostgresUserStore) GetProfileInTransaction(ctx context.Context, tx pgx.Tx, id string) (*userstore.Profile, error) {
	return getProfile(ctx, tx, s.userID, id)
}

func getProfile(ctx context.Context, exec preferenceSettingsExecutor, userID int, id string) (*userstore.Profile, error) {
	row := exec.QueryRow(ctx, `
		SELECT id, name, avatar, pin_hash, COALESCE(login_email, ''), credential_revision, is_child, is_primary, max_content_rating,
		       quality_preference, language, preferred_metadata_language, subtitle_language, subtitle_mode,
		       auto_skip_intro, auto_skip_credits, auto_skip_recap, auto_play_next_preview, library_restrictions_enabled,
		       show_forced_subtitles, max_playback_quality, organization_id::text, access_group_id, created_at, updated_at
		FROM user_profiles WHERE user_id = $1 AND id = $2`, userID, id)

	p, err := scanProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying profile %s: %w", id, err)
	}
	p.AllowedLibraryIDs, err = listProfileAllowedLibraries(ctx, exec, userID, p.ID)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *PostgresUserStore) ListProfiles(ctx context.Context) ([]userstore.Profile, error) {
	return listProfiles(ctx, s.pool, s.userID)
}

// ListProfilesInTransaction lists profiles through a caller-owned transaction.
func (s *PostgresUserStore) ListProfilesInTransaction(ctx context.Context, tx pgx.Tx) ([]userstore.Profile, error) {
	return listProfiles(ctx, tx, s.userID)
}

func listProfiles(ctx context.Context, exec preferenceSettingsExecutor, userID int) ([]userstore.Profile, error) {
	rows, err := exec.Query(ctx, `
		SELECT id, name, avatar, pin_hash, COALESCE(login_email, ''), credential_revision, is_child, is_primary, max_content_rating,
		       quality_preference, language, preferred_metadata_language, subtitle_language, subtitle_mode,
		       auto_skip_intro, auto_skip_credits, auto_skip_recap, auto_play_next_preview, library_restrictions_enabled,
		       show_forced_subtitles, max_playback_quality, organization_id::text, access_group_id, created_at, updated_at
		FROM user_profiles WHERE user_id = $1 ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing profiles: %w", err)
	}
	defer rows.Close()

	var profiles []userstore.Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning profile row: %w", err)
		}
		profiles = append(profiles, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating profile rows: %w", err)
	}
	if err := attachAllowedLibraries(ctx, exec, userID, profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

func (s *PostgresUserStore) UpdateProfile(ctx context.Context, id string, u userstore.UpdateProfileInput) error {
	return updateProfile(ctx, s.pool, s.userID, id, u)
}

func updateProfile(
	ctx context.Context,
	exec preferenceSettingsExecutor,
	userID int,
	id string,
	u userstore.UpdateProfileInput,
) error {
	var setClauses []string
	var args []any
	argIdx := 1
	accessPolicyChanged := u.PIN != nil ||
		u.IsChild != nil ||
		u.MaxContentRating != nil ||
		u.LibraryRestrictionsEnabled != nil ||
		u.AllowedLibraryIDs != nil ||
		u.MaxPlaybackQuality != nil

	addArg := func(clause string, val any) {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", clause, argIdx))
		args = append(args, val)
		argIdx++
	}

	if u.Name != nil {
		addArg("name", *u.Name)
	}
	if u.Avatar != nil {
		addArg("avatar", *u.Avatar)
	}
	if u.PIN != nil {
		if *u.PIN == "" {
			// Empty string clears the PIN rather than hashing an empty value.
			addArg("pin_hash", "")
		} else {
			hash, err := bcrypt.GenerateFromPassword([]byte(*u.PIN), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("hashing PIN: %w", err)
			}
			addArg("pin_hash", string(hash))
		}
	}
	if u.IsChild != nil {
		addArg("is_child", *u.IsChild)
	}
	if u.MaxContentRating != nil {
		addArg("max_content_rating", *u.MaxContentRating)
	}
	if u.QualityPreference != nil {
		addArg("quality_preference", *u.QualityPreference)
	}
	if u.Language != nil {
		addArg("language", *u.Language)
	}
	if u.PreferredMetadataLanguage != nil {
		addArg("preferred_metadata_language", *u.PreferredMetadataLanguage)
	}
	if u.SubtitleLanguage != nil {
		addArg("subtitle_language", *u.SubtitleLanguage)
	}
	if u.SubtitleMode != nil {
		addArg("subtitle_mode", *u.SubtitleMode)
	}
	if u.AutoSkipIntro != nil {
		addArg("auto_skip_intro", *u.AutoSkipIntro)
	}
	if u.AutoSkipCredits != nil {
		addArg("auto_skip_credits", *u.AutoSkipCredits)
	}
	if u.AutoSkipRecap != nil {
		addArg("auto_skip_recap", *u.AutoSkipRecap)
	}
	if u.AutoPlayNextPreview != nil {
		addArg("auto_play_next_preview", *u.AutoPlayNextPreview)
	}
	if u.LibraryRestrictionsEnabled != nil {
		addArg("library_restrictions_enabled", *u.LibraryRestrictionsEnabled)
	}
	if u.ShowForcedSubtitles != nil {
		addArg("show_forced_subtitles", *u.ShowForcedSubtitles)
	}
	if u.MaxPlaybackQuality != nil {
		addArg("max_playback_quality", *u.MaxPlaybackQuality)
	}

	if len(setClauses) == 0 && u.AllowedLibraryIDs == nil {
		return nil
	}

	var exists bool
	if err := exec.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM user_profiles WHERE user_id = $1 AND id = $2)", userID, id).Scan(&exists); err != nil {
		return fmt.Errorf("checking profile %s existence: %w", id, err)
	}
	if !exists {
		return fmt.Errorf("profile %s not found", id)
	}

	if len(setClauses) > 0 {
		addArg("updated_at", nowUTC())

		whereClause := fmt.Sprintf("WHERE user_id = $%d AND id = $%d", argIdx, argIdx+1)
		args = append(args, userID, id)

		query := fmt.Sprintf("UPDATE user_profiles SET %s %s", strings.Join(setClauses, ", "), whereClause)
		result, err := exec.Exec(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("updating profile %s: %w", id, err)
		}
		if result.RowsAffected() == 0 {
			return fmt.Errorf("profile %s not found", id)
		}
	}

	if u.AllowedLibraryIDs != nil {
		if err := replaceProfileAllowedLibraries(ctx, exec, userID, id, *u.AllowedLibraryIDs); err != nil {
			return err
		}
	}
	if accessPolicyChanged {
		if _, err := exec.Exec(ctx, "UPDATE users SET access_policy_revision = access_policy_revision + 1 WHERE id = $1", userID); err != nil {
			return fmt.Errorf("bumping access policy revision for user %d: %w", userID, err)
		}
	}
	return nil
}

func (s *PostgresUserStore) DeleteProfile(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction for profile delete: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := deleteProfile(ctx, tx, s.userID, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteProfileInTransaction deletes a profile through a caller-owned transaction.
func (s *PostgresUserStore) DeleteProfileInTransaction(ctx context.Context, tx pgx.Tx, id string) error {
	return deleteProfile(ctx, tx, s.userID, id)
}

func deleteProfile(ctx context.Context, tx preferenceSettingsExecutor, userID int, id string) error {
	// Preferences belong to viewers, not creators, so remove every override
	// for collections owned by the profile before those collections disappear.
	if _, err := tx.Exec(ctx, `
		DELETE FROM user_collection_sort_preferences
		WHERE user_id = $1 AND collection_kind = 'user' AND collection_id IN (
			SELECT id FROM user_personal_collections WHERE user_id = $1 AND profile_id = $2
		)`, userID, id); err != nil {
		return fmt.Errorf("deleting collection sort preferences for profile %s: %w", id, err)
	}

	_, err := tx.Exec(ctx, `
		DELETE FROM user_personal_collection_items
		WHERE user_id = $1 AND collection_id IN (
			SELECT id FROM user_personal_collections WHERE user_id = $1 AND profile_id = $2
		)`, userID, id)
	if err != nil {
		return fmt.Errorf("deleting collection items for profile %s: %w", id, err)
	}

	// user_setting_values is listed rather than left to its composite profile
	// FK so both backends delete identically: the per-user SQLite store has no
	// foreign keys at all. Account-scope rows carry a NULL profile_id and are
	// untouched, which is what removing one household member has to mean.
	cascadeTables := []string{
		"user_favorites",
		"user_watchlist",
		"user_watch_progress",
		"user_device_settings",
		"user_devices",
		"user_collection_sort_preferences",
		"user_personal_collections",
		"user_series_playback_preferences",
		"user_library_playback_preferences",
		"user_setting_values",
	}
	for _, table := range cascadeTables {
		if _, err := tx.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE user_id = $1 AND profile_id = $2", table), userID, id); err != nil {
			return fmt.Errorf("deleting from %s for profile %s: %w", table, id, err)
		}
	}

	result, err := tx.Exec(ctx, "DELETE FROM user_profiles WHERE user_id = $1 AND id = $2", userID, id)
	if err != nil {
		return fmt.Errorf("deleting profile %s: %w", id, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("profile %s not found", id)
	}

	return nil
}

func (s *PostgresUserStore) VerifyPIN(ctx context.Context, profileID, pin string) (bool, error) {
	var pinHash string
	err := s.pool.QueryRow(ctx,
		"SELECT pin_hash FROM user_profiles WHERE user_id = $1 AND id = $2",
		s.userID, profileID,
	).Scan(&pinHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("profile %s not found", profileID)
	}
	if err != nil {
		return false, fmt.Errorf("querying PIN hash for profile %s: %w", profileID, err)
	}
	if pinHash == "" {
		return false, fmt.Errorf("profile %s has no PIN set", profileID)
	}

	err = bcrypt.CompareHashAndPassword([]byte(pinHash), []byte(pin))
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("comparing PIN for profile %s: %w", profileID, err)
	}
	return true, nil
}
