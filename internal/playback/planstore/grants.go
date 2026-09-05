package planstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/playback"
)

var _ playback.GrantPlanStoreV3 = (*Postgres)(nil)

// NewPostgresWithGrantPolicy enables only the durable grant operations. The
// ordinary constructor remains unconfigured; no lifecycle caller opts in yet.
func NewPostgresWithGrantPolicy(db *pgxpool.Pool, policy playback.AttemptGrantPolicyV3) (*Postgres, error) {
	if policy.MaxDuration < time.Microsecond {
		return nil, fmt.Errorf("invalid playback grant duration policy")
	}
	return &Postgres{db: db, grantMaxDuration: policy.MaxDuration}, nil
}

// StageAttemptRoute freezes the existing recipe/plan before preparation can
// receive execution authority. Replays are allowed only for the same binding.
func (s *Postgres) StageAttemptRoute(ctx context.Context, authority playback.AttemptAuthorityV3, record playback.AttemptRecordV3, route playback.AttemptGrantRouteV3) error {
	if record.PlaybackAttemptID != authority.PlaybackAttemptID || record.SessionID == "" || record.CurrentPlan.SessionID != record.SessionID || record.CurrentPlanID == "" || record.CurrentPlanID != record.CurrentPlan.PlanID || !record.FrozenRecipe.ValidFor(record.CurrentPlan) || record.CurrentReplanRequestID != "" || route.TransportID == "" || route.ExecutionNodeID < 0 || route.EgressNodeID < 0 {
		return fmt.Errorf("invalid prepared playback route")
	}
	plan, err := json.Marshal(record.CurrentPlan)
	if err != nil {
		return err
	}
	recipe, err := json.Marshal(record.FrozenRecipe)
	if err != nil {
		return err
	}
	binding, err := json.Marshal(route)
	if err != nil {
		return err
	}
	return s.withAuthorityLock(ctx, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET
		 session_id = $5::uuid, effective_media_file_id = $6, current_plan_id = $7,
		 current_plan = $8, frozen_recipe = $9, control_route = $10, updated_at = clock_timestamp()
		 WHERE playback_attempt_id = $1 AND control_incarnation = NULLIF($2, '')::uuid AND control_owner = $3::uuid AND control_epoch = $4
		 AND control_state = 'preparing' AND control_lease_expires_at > clock_timestamp() AND expires_at > clock_timestamp()
		 AND user_id = $11 AND profile_id = $12 AND requested_media_file_id = $13 AND request_digest = $14
		 AND (control_route IS NULL OR (session_id = $5::uuid AND effective_media_file_id = $6 AND current_plan_id = $7
		 AND current_plan = $8::jsonb AND frozen_recipe = $9::jsonb AND control_route = $10::jsonb))`,
			authority.PlaybackAttemptID, authority.Incarnation, authority.OwnerID, authority.Epoch,
			record.SessionID, record.EffectiveMediaFileID, record.CurrentPlanID, plan, recipe, binding,
			record.UserID, record.ProfileID, record.RequestedMediaFileID, record.RequestDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return playback.ErrStaleAttemptAuthorityV3
		}
		return nil
	})
}

// IssueAttemptGrant commits the maximum deadline before returning. Losing this
// response must never let drain assume the grant was not issued.
func (s *Postgres) IssueAttemptGrant(ctx context.Context, authority playback.AttemptAuthorityV3, request playback.AttemptGrantRequestV3) (playback.AttemptGrantV3, error) {
	var grant playback.AttemptGrantV3
	if s.grantMaxDuration < time.Microsecond || request.Duration < time.Microsecond {
		return grant, fmt.Errorf("playback grant policy or duration is not configured")
	}
	if request.Purpose != playback.AttemptGrantExecuteV3 && request.Purpose != playback.AttemptGrantServeV3 {
		return grant, fmt.Errorf("invalid playback grant purpose")
	}
	grant.Authority, grant.Request = authority, request
	err := s.withAuthorityLock(ctx, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `WITH timing AS MATERIALIZED (SELECT clock_timestamp() AS now)
		 UPDATE playback_v3_attempts SET control_grant_not_after = GREATEST(control_grant_not_after,
		 LEAST(timing.now + $5 * interval '1 microsecond', control_lease_expires_at, expires_at)), updated_at = timing.now
		 FROM timing
		 WHERE playback_attempt_id = $1 AND control_incarnation = NULLIF($2, '')::uuid AND control_owner = $3::uuid AND control_epoch = $4
		 AND control_lease_expires_at > timing.now AND expires_at > timing.now
		 AND session_id = NULLIF($6, '')::uuid AND current_plan_id = $7 AND control_route->>'transport_id' = $8
		 AND (($9 = 'execute' AND control_state IN ('preparing', 'active') AND (control_route->>'execution_node_id')::bigint = $10)
		 OR ($9 = 'serve' AND control_state = 'active' AND (control_route->>'egress_node_id')::bigint = $10))
		 RETURNING control_state, control_lease_expires_at, timing.now, LEAST(timing.now + $5 * interval '1 microsecond', control_lease_expires_at, expires_at)`,
			authority.PlaybackAttemptID, authority.Incarnation, authority.OwnerID, authority.Epoch,
			min(request.Duration, s.grantMaxDuration).Microseconds(), request.SessionID, request.PlanID, request.TransportID, request.Purpose, request.NodeID).Scan(
			&grant.Authority.State, &grant.Authority.LeaseExpiresAt, &grant.IssuedAt, &grant.NotAfter)
	})
	if err != nil {
		return playback.AttemptGrantV3{}, grantError(err)
	}
	return grant, nil
}

// BeginAttemptDrain closes issuance without claiming that a runtime has stopped.
// The same incarnation/epoch can initiate drain after its owner lease expires.
func (s *Postgres) BeginAttemptDrain(ctx context.Context, authority playback.AttemptAuthorityV3) (playback.AttemptDrainV3, error) {
	var drain playback.AttemptDrainV3
	err := s.withAuthorityLock(ctx, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE playback_v3_attempts SET control_state = 'draining',
		 control_drain_not_before = COALESCE(control_drain_not_before, GREATEST(clock_timestamp(), control_grant_not_after)), updated_at = clock_timestamp()
		 WHERE playback_attempt_id = $1 AND control_incarnation = NULLIF($2, '')::uuid AND control_owner = $3::uuid AND control_epoch = $4
		 AND control_state IN ('preparing', 'active', 'draining') AND expires_at > clock_timestamp()
		 RETURNING control_drain_not_before`, authority.PlaybackAttemptID, authority.Incarnation, authority.OwnerID, authority.Epoch).Scan(&drain.NotBefore)
	})
	if err != nil {
		return playback.AttemptDrainV3{}, grantError(err)
	}
	return drain, nil
}

// CompleteAttemptDrain records that the stored grant deadline has passed. It
// creates no successor and does not assert process exit or response revocation.
func (s *Postgres) CompleteAttemptDrain(ctx context.Context, authority playback.AttemptAuthorityV3) error {
	return s.withAuthorityLock(ctx, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET control_state = 'stopped', updated_at = clock_timestamp()
		 WHERE playback_attempt_id = $1 AND control_incarnation = NULLIF($2, '')::uuid AND control_owner = $3::uuid AND control_epoch = $4
		 AND control_state IN ('draining', 'stopped') AND control_drain_not_before <= clock_timestamp()`,
			authority.PlaybackAttemptID, authority.Incarnation, authority.OwnerID, authority.Epoch)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return playback.ErrStaleAttemptAuthorityV3
		}
		return nil
	})
}

func grantError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return playback.ErrStaleAttemptAuthorityV3
	}
	return err
}
