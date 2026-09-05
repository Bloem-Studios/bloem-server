package planstore

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/playback"
)

var _ playback.ExecutorRecipePlanStoreV3 = (*Postgres)(nil)

func (s *Postgres) PublishAttemptRecipeLocator(ctx context.Context, authority playback.AttemptAuthorityV3, expected *playback.ExecutorRecipeLocatorV3, next playback.ExecutorRecipeLocatorV3) error {
	if err := next.Validate(); err != nil {
		return err
	}
	if next.Executor.Incarnation != authority.Incarnation || next.Executor.Epoch != authority.Epoch {
		return playback.ErrStaleAttemptAuthorityV3
	}
	// An immutable executor key cannot legitimately change its recipe bytes.
	// A replacement requires a separately staged namespace, never a new digest
	// under the already published executor generation.
	if expected != nil && *expected != next {
		return playback.ErrStaleAttemptAuthorityV3
	}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	var previous []byte
	if expected != nil {
		previous = data
	}
	return s.withAuthorityLock(ctx, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET control_recipe_locator = $5, updated_at = clock_timestamp()
		 WHERE playback_attempt_id = $1 AND control_incarnation = NULLIF($2, '')::uuid AND control_owner = $3::uuid AND control_epoch = $4
		 AND control_state IN ('preparing', 'active') AND control_lease_expires_at > clock_timestamp() AND expires_at > clock_timestamp()
		 AND control_route->'executor' = $5::jsonb->'executor'
		 AND control_recipe_locator IS NOT DISTINCT FROM $6::jsonb`, authority.PlaybackAttemptID, authority.Incarnation, authority.OwnerID, authority.Epoch, data, previous)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return playback.ErrStaleAttemptAuthorityV3
		}
		return nil
	})
}

// GetAttemptRecipeLocator returns only the current fenced binding, never a
// Redis session-key guess. The caller still needs execution/serving authority.
func (s *Postgres) GetAttemptRecipeLocator(ctx context.Context, authority playback.AttemptAuthorityV3) (*playback.ExecutorRecipeLocatorV3, error) {
	var data []byte
	err := s.db.QueryRow(ctx, `SELECT control_recipe_locator FROM playback_v3_attempts
	 WHERE playback_attempt_id = $1 AND control_incarnation = NULLIF($2, '')::uuid AND control_owner = $3::uuid AND control_epoch = $4
	 AND control_state IN ('preparing', 'active') AND control_lease_expires_at > clock_timestamp() AND expires_at > clock_timestamp()
	 AND control_recipe_locator IS NOT NULL`, authority.PlaybackAttemptID, authority.Incarnation, authority.OwnerID, authority.Epoch).Scan(&data)
	if err != nil {
		return nil, grantError(err)
	}
	var locator playback.ExecutorRecipeLocatorV3
	if err := json.Unmarshal(data, &locator); err != nil {
		return nil, err
	}
	if err := locator.Validate(); err != nil {
		return nil, err
	}
	return &locator, nil
}
