package planstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/playback"
)

var _ playback.AuthoritativePlanStoreV3 = (*Postgres)(nil)

// ReserveAttempt reserves the existing attempt row before transport allocation.
// Only preparing reservations can be taken over; this does not enable failover
// of a serving transport. All expiry decisions use the database clock.
func (s *Postgres) ReserveAttempt(ctx context.Context, request playback.AttemptReservationRequestV3) (playback.AttemptReservationV3, error) {
	var result playback.AttemptReservationV3
	if request.PlaybackAttemptID == "" || request.UserID <= 0 || request.ProfileID == "" || request.RequestedMediaFileID <= 0 || request.RequestDigest == "" || request.LeaseDuration < time.Microsecond || request.Retention < request.LeaseDuration {
		return result, fmt.Errorf("invalid playback attempt reservation")
	}
	if _, err := uuid.Parse(request.OwnerID); err != nil {
		return result, fmt.Errorf("invalid playback authority owner: %w", err)
	}
	normalized, err := json.Marshal(request.NormalizedRequest)
	if err != nil {
		return result, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer rollbackAuthority(tx)
	inserted, err := tx.Exec(ctx, `
		WITH timing AS MATERIALIZED (SELECT clock_timestamp() AS now)
		INSERT INTO playback_v3_attempts (
		 playback_attempt_id, user_id, profile_id, requested_media_file_id,
		 effective_media_file_id, current_plan_id, current_plan, frozen_recipe,
		 normalized_request, request_digest, expires_at,
		 control_state, control_owner, control_epoch, control_lease_expires_at
		) SELECT $1, $2, $3, $4, $4, '', '{}', '{}', $5, $6,
		 timing.now + $9 * interval '1 microsecond',
		 'preparing', $7::uuid, 1, timing.now + $8 * interval '1 microsecond' FROM timing
		ON CONFLICT (playback_attempt_id) DO NOTHING`,
		request.PlaybackAttemptID, request.UserID, request.ProfileID, request.RequestedMediaFileID,
		normalized, request.RequestDigest, request.OwnerID, request.LeaseDuration.Microseconds(), request.Retention.Microseconds())
	if err != nil {
		return result, err
	}
	var userID, fileID int
	var profileID, digest string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT user_id, profile_id, requested_media_file_id, request_digest,
		 control_state, COALESCE(control_owner::text, ''), control_epoch, COALESCE(control_lease_expires_at, 'epoch'::timestamptz),
		 expires_at
		FROM playback_v3_attempts WHERE playback_attempt_id = $1 FOR UPDATE`, request.PlaybackAttemptID).Scan(
		&userID, &profileID, &fileID, &digest, &result.Authority.State, &result.Authority.OwnerID,
		&result.Authority.Epoch, &result.Authority.LeaseExpiresAt, &expiresAt)
	if err != nil {
		return result, err
	}
	if userID != request.UserID || profileID != request.ProfileID || fileID != request.RequestedMediaFileID || digest != request.RequestDigest {
		return playback.AttemptReservationV3{}, playback.ErrIdempotencyKeyReusedV3
	}
	if result.Authority.State == "legacy" {
		return playback.AttemptReservationV3{}, playback.ErrPlaybackAttemptExistsV3
	}
	result.Authority.PlaybackAttemptID = request.PlaybackAttemptID
	var dbNow time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		return playback.AttemptReservationV3{}, err
	}
	if !expiresAt.After(dbNow) {
		return playback.AttemptReservationV3{}, playback.ErrSessionNotFound
	}
	result.Owned = inserted.RowsAffected() == 1
	if !result.Owned && result.Authority.State == playback.AttemptPreparingV3 && !result.Authority.LeaseExpiresAt.After(dbNow) {
		err = tx.QueryRow(ctx, `UPDATE playback_v3_attempts SET
		 control_owner = $2::uuid, control_epoch = control_epoch + 1,
		 control_lease_expires_at = LEAST(expires_at, clock_timestamp() + $3 * interval '1 microsecond'), updated_at = clock_timestamp()
		 WHERE playback_attempt_id = $1
		 RETURNING control_owner::text, control_epoch, control_lease_expires_at`, request.PlaybackAttemptID, request.OwnerID, request.LeaseDuration.Microseconds()).Scan(
			&result.Authority.OwnerID, &result.Authority.Epoch, &result.Authority.LeaseExpiresAt)
		if err != nil {
			return playback.AttemptReservationV3{}, err
		}
		result.Owned = true
	}
	result.Record, err = scanAttempt(tx.QueryRow(ctx, attemptSelect+` WHERE playback_attempt_id = $1`, request.PlaybackAttemptID))
	if err != nil {
		return playback.AttemptReservationV3{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return playback.AttemptReservationV3{}, err
	}
	return result, nil
}

func (s *Postgres) RenewAttempt(ctx context.Context, authority playback.AttemptAuthorityV3, duration time.Duration) (playback.AttemptAuthorityV3, error) {
	if duration < time.Microsecond {
		return playback.AttemptAuthorityV3{}, fmt.Errorf("invalid playback authority lease duration")
	}
	err := s.withAuthorityLock(ctx, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE playback_v3_attempts SET
	 control_lease_expires_at = LEAST(expires_at, clock_timestamp() + $4 * interval '1 microsecond'), updated_at = clock_timestamp()
	 WHERE playback_attempt_id = $1 AND control_owner = $2::uuid AND control_epoch = $3
	 AND control_state IN ('preparing', 'active') AND control_lease_expires_at > clock_timestamp() AND expires_at > clock_timestamp()
	 RETURNING control_state, control_lease_expires_at`, authority.PlaybackAttemptID, authority.OwnerID, authority.Epoch, duration.Microseconds()).Scan(&authority.State, &authority.LeaseExpiresAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return playback.AttemptAuthorityV3{}, playback.ErrStaleAttemptAuthorityV3
	}
	return authority, err
}

// PublishAttempt commits the existing response and recipe together with the
// preparing-to-active/terminal transition. It never allocates or publishes a
// worker grant itself. Repeating an uncertain publication must first replay the
// reservation; this CAS cannot overwrite a committed or stopped decision.
func (s *Postgres) PublishAttempt(ctx context.Context, authority playback.AttemptAuthorityV3, record playback.AttemptRecordV3) error {
	state := playback.AttemptTerminalV3
	switch record.StartResponse.Outcome {
	case playback.OutcomePlayableV3:
		state = playback.AttemptActiveV3
		if record.SessionID == "" || record.StartResponse.SessionID != record.SessionID || record.CurrentPlan.SessionID != record.SessionID || record.StartResponse.PlaybackPlan == nil || record.CurrentPlanID != record.StartResponse.PlaybackPlan.PlanID {
			return fmt.Errorf("inconsistent playable attempt")
		}
	case playback.OutcomeAdaptationUnavailableV3:
		if record.SessionID != "" || record.StartResponse.SessionID != "" || record.StartResponse.PlaybackPlan != nil {
			return fmt.Errorf("terminal attempt has a session")
		}
	default:
		return fmt.Errorf("invalid playback attempt outcome")
	}
	if record.PlaybackAttemptID != authority.PlaybackAttemptID {
		return playback.ErrIdempotencyKeyReusedV3
	}
	plan, err := json.Marshal(record.CurrentPlan)
	if err != nil {
		return err
	}
	recipe, err := json.Marshal(record.FrozenRecipe)
	if err != nil {
		return err
	}
	response, err := json.Marshal(record.StartResponse)
	if err != nil {
		return err
	}
	return s.withAuthorityLock(ctx, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET
	 session_id = NULLIF($4, '')::uuid, effective_media_file_id = $5, current_plan_id = $6,
	 current_plan = $7, frozen_recipe = $8, start_response = $9, control_state = $10, updated_at = clock_timestamp()
	 WHERE playback_attempt_id = $1 AND control_owner = $2::uuid AND control_epoch = $3
	 AND control_state = 'preparing' AND control_lease_expires_at > clock_timestamp() AND expires_at > clock_timestamp()
	 AND user_id = $11 AND profile_id = $12 AND requested_media_file_id = $13 AND request_digest = $14`,
			authority.PlaybackAttemptID, authority.OwnerID, authority.Epoch, record.SessionID, record.EffectiveMediaFileID, record.CurrentPlanID,
			plan, recipe, response, state, record.UserID, record.ProfileID, record.RequestedMediaFileID, record.RequestDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return playback.ErrStaleAttemptAuthorityV3
		}
		return nil
	})
}

// StopAttempt retains the same row as a tombstone through its replay retention.
// This fences database publication only; transport revocation is not activated.
func (s *Postgres) StopAttempt(ctx context.Context, authority playback.AttemptAuthorityV3) error {
	return s.withAuthorityLock(ctx, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET control_state = 'stopped', updated_at = clock_timestamp()
	 WHERE playback_attempt_id = $1 AND control_owner = $2::uuid AND control_epoch = $3
	 AND control_state IN ('preparing', 'active', 'stopped')
	 AND control_lease_expires_at > clock_timestamp() AND expires_at > clock_timestamp()`, authority.PlaybackAttemptID, authority.OwnerID, authority.Epoch)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return playback.ErrStaleAttemptAuthorityV3
		}
		return nil
	})
}

// Acquire the lock before the mutation statement evaluates database time. A
// statement waiting on a row lock must not renew a lease using a pre-wait check.
func (s *Postgres) withAuthorityLock(ctx context.Context, attemptID string, mutate func(pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackAuthority(tx)
	var id string
	if err := tx.QueryRow(ctx, `SELECT playback_attempt_id FROM playback_v3_attempts WHERE playback_attempt_id = $1 FOR UPDATE`, attemptID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return playback.ErrStaleAttemptAuthorityV3
		}
		return err
	}
	if err := mutate(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func rollbackAuthority(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
