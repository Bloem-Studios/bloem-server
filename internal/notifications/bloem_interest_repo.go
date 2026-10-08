package notifications

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// bloemInterestTenancy is Bloem's opt-in tenancy filter for
// InterestRepository. Without WithBloemTenancy the repository runs Silo's
// candidate query unchanged.
type bloemInterestTenancy struct {
	tenancy bool
}

// WithBloemTenancy makes ListActiveBySeries re-verify each candidate's
// organization access to the event's library at read time.
func (r *InterestRepository) WithBloemTenancy() *InterestRepository {
	r.tenancy = true
	return r
}

// listActiveBySeriesForTenants is ListActiveBySeries with Bloem's tenancy
// join.
//
// A row's library_id is trusted only up to the moment it was written: an
// interest row is not recomputed or invalidated when an entitlement is later
// revoked, so this join re-verifies at read time that the recipient's
// CURRENT organization still owns, or holds an ACTIVE entitlement to,
// library_id — the same join shape as
// resourcetenancy.Store.AvailableMediaFolderIDs. A stale row naming another
// tenant's library, or a library whose entitlement was revoked since the row
// was written, is excluded rather than fanned out to.
func (r *InterestRepository) listActiveBySeriesForTenants(ctx context.Context, tx pgx.Tx, libraryID int, seriesID string) ([]SeriesInterest, error) {
	rows, err := tx.Query(ctx, `
		SELECT psi.user_id, psi.profile_id, psi.library_id, psi.series_id,
		       psi.favorite, psi.watchlist, psi.continue_watching, psi.next_up_candidate,
		       psi.last_completed_episode_key, psi.next_expected_episode_key, psi.last_notified_episode_key,
		       psi.updated_at
		FROM profile_series_interest psi
		JOIN user_profiles prof ON prof.id = psi.profile_id
		JOIN organizations org ON org.id = prof.organization_id AND org.status = 'active'
		JOIN media_folders folders ON folders.id = psi.library_id
		JOIN resource_owners owners ON owners.id = folders.owner_id
		LEFT JOIN organization_entitlements ent
		  ON ent.organization_id = prof.organization_id
		 AND ent.root_owner_id = owners.id
		 AND ent.media_folder_id = folders.id
		 AND ent.status = 'active'
		WHERE psi.library_id = $1 AND psi.series_id = $2
		  AND (psi.favorite OR psi.watchlist OR psi.continue_watching OR psi.next_up_candidate)
		  AND ((owners.kind = 'organization' AND owners.organization_id = prof.organization_id)
		       OR (owners.kind = 'platform' AND ent.id IS NOT NULL))`,
		libraryID, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list series interest: %w", err)
	}
	defer rows.Close()

	interests := make([]SeriesInterest, 0, 16)
	for rows.Next() {
		var interest SeriesInterest
		if err := rows.Scan(
			&interest.UserID, &interest.ProfileID, &interest.LibraryID, &interest.SeriesID,
			&interest.Favorite, &interest.Watchlist, &interest.ContinueWatching, &interest.NextUpCandidate,
			&interest.LastCompletedEpisodeKey, &interest.NextExpectedEpisodeKey, &interest.LastNotifiedEpisodeKey,
			&interest.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan series interest: %w", err)
		}
		interests = append(interests, interest)
	}
	return interests, rows.Err()
}
