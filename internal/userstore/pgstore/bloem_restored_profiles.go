package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// bloemCreateRestoredProfileInTx is CreateRestoredProfileInTx on Bloem's
// schema, where every profile belongs to its account's organization and an
// access group there. The profile joins the organization of the account's
// membership, in the group the backup names when that organization has one
// by that name, else the account's group there, else the organization's
// default group. It always handles the write.
func bloemCreateRestoredProfileInTx(ctx context.Context, tx pgx.Tx, userID int, p RestoredProfile) (bool, error) {
	var organizationID string
	var groupID *int64
	err := tx.QueryRow(ctx, `
		SELECT m.organization_id::text,
			(SELECT g.id FROM access_groups g WHERE g.id = u.access_group_id AND g.organization_id = m.organization_id)
		FROM organization_memberships m JOIN users u ON u.id = m.account_id
		WHERE m.account_id = $1`, userID).Scan(&organizationID, &groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, fmt.Errorf("restoring profile %s: account %d belongs to no organization", p.ID, userID)
	}
	if err != nil {
		return true, fmt.Errorf("reading the organization of account %d: %w", userID, err)
	}
	if p.BloemAccessGroup != "" {
		var named int64
		err := tx.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id = $1 AND name = $2`,
			organizationID, p.BloemAccessGroup).Scan(&named)
		switch {
		case err == nil:
			groupID = &named
		case !errors.Is(err, pgx.ErrNoRows):
			return true, fmt.Errorf("finding access group %q: %w", p.BloemAccessGroup, err)
		}
	}
	if groupID == nil {
		var fallback int64
		err := tx.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id = $1 AND is_default`, organizationID).Scan(&fallback)
		if errors.Is(err, pgx.ErrNoRows) {
			return true, fmt.Errorf("restoring profile %s: organization %s has no default access group", p.ID, organizationID)
		}
		if err != nil {
			return true, fmt.Errorf("finding the default access group of organization %s: %w", organizationID, err)
		}
		groupID = &fallback
	}
	var hasPrimary bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_profiles WHERE user_id = $1 AND is_primary)`, userID).Scan(&hasPrimary); err != nil {
		return true, fmt.Errorf("checking the primary profile of user %d: %w", userID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profiles (id, user_id, name, avatar, pin_hash, is_child, is_primary, max_content_rating, max_advisory_age,
			require_advisory_age, library_restrictions_enabled, max_playback_quality, organization_id, access_group_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9::smallint, 0), $10, $11, $12, $13::uuid, $14)`,
		p.ID, userID, p.Name, p.Avatar, p.PINHash, p.IsChild, p.IsPrimary && !hasPrimary, p.MaxContentRating, p.MaxAdvisoryAge,
		p.RequireAdvisoryAge, p.LibraryRestrictionsEnabled, p.MaxPlaybackQuality, organizationID, *groupID); err != nil {
		return true, fmt.Errorf("inserting restored profile: %w", err)
	}
	return true, replaceProfileAllowedLibraries(ctx, tx, userID, p.ID, p.AllowedLibraryIDs)
}
