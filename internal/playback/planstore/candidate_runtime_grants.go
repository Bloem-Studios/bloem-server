package planstore

import (
	"context"
	"encoding/json"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/jackc/pgx/v5"
)

// withGrantAuthority checks selected-source admission under the same registration
// lock used by withdrawal. Unbound attempts cannot gain a binding between the
// discovery read and the locked mutation.
func (s *Postgres) withGrantAuthority(ctx context.Context, authority playback.AttemptAuthorityV3, fn func(pgx.Tx) error) error {
	var data []byte
	if err := s.db.QueryRow(ctx, `SELECT control_activation FROM playback_v3_attempts WHERE playback_attempt_id=$1`, authority.PlaybackAttemptID).Scan(&data); err != nil {
		return err
	}
	if len(data) == 0 {
		return s.withAuthorityLock(ctx, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
			var unbound bool
			if err := tx.QueryRow(ctx, `SELECT control_activation IS NULL FROM playback_v3_attempts WHERE playback_attempt_id=$1`, authority.PlaybackAttemptID).Scan(&unbound); err != nil {
				return err
			}
			if !unbound {
				return playback.ErrStaleAttemptAuthorityV3
			}
			return fn(tx)
		})
	}
	if len(data) > initialActivationDocumentLimit {
		return playback.ErrStaleAttemptAuthorityV3
	}
	var activation playback.InitialActivationV3
	if err := json.Unmarshal(data, &activation); err != nil {
		return err
	}
	if activation.Binding.Authority().PlaybackAttemptID != authority.PlaybackAttemptID || activation.Binding.Fence.Incarnation != authority.Incarnation || activation.Binding.Fence.OwnerID != authority.OwnerID || activation.Binding.Fence.Epoch != authority.Epoch {
		return playback.ErrStaleAttemptAuthorityV3
	}
	_, err := s.withInitialActivation(ctx, activation.Binding, func(tx pgx.Tx, row *initialActivationRow) (playback.InitialActivationV3, error) {
		if row.activation == nil || !row.admitting || !row.live() {
			return playback.InitialActivationV3{}, playback.ErrStaleAttemptAuthorityV3
		}
		return *row.activation, fn(tx)
	})
	return err
}

// candidateBinding reads immutable input only. Issuance rechecks the retained
// replacement under locks; this lookup never grants serving or transfer rights.
func (s *ExecutorRuntime) candidateBinding(ctx context.Context, transportID string, executor playback.ExecutorNamespaceV3) (playback.AttemptAuthorityV3, playback.AttemptGrantRequestV3, *playback.ExecutorRecipeLocatorV3, error) {
	var authority playback.AttemptAuthorityV3
	request := playback.AttemptGrantRequestV3{Executor: executor, TransportID: transportID, NodeID: s.nodeID, Duration: s.policy.MaxDuration}
	if executor.Validate() != nil || transportID == "" {
		return authority, request, nil, playback.ErrStaleAttemptAuthorityV3
	}
	encoded, err := json.Marshal(executor)
	if err != nil {
		return authority, request, nil, err
	}
	var data []byte
	err = s.store.db.QueryRow(ctx, `SELECT a.playback_attempt_id,a.control_incarnation::text,a.control_owner::text,a.control_epoch,a.control_state,a.control_lease_expires_at,a.session_id::text,r.route_replacement->'next'->>'CurrentPlanID',r.route_replacement->'locator'
 FROM playback_v3_attempts a JOIN playback_v3_replans r ON r.session_id=a.session_id
 WHERE a.control_incarnation=$1::uuid AND a.control_epoch=$2 AND a.control_state='active'
 AND a.control_lease_expires_at>clock_timestamp() AND a.expires_at>clock_timestamp()
 AND a.control_activation->>'phase'='activated'
 AND r.route_replacement->>'phase' IN ('staged','ready','retiring')
 AND r.route_replacement->'route'->'executor'=$3::jsonb
 AND r.route_replacement->'route'->>'transport_id'=$4
 AND (r.route_replacement->'route'->>'execution_node_id')::bigint=$5`, executor.Incarnation, executor.Epoch, encoded, transportID, s.nodeID).Scan(&authority.PlaybackAttemptID, &authority.Incarnation, &authority.OwnerID, &authority.Epoch, &authority.State, &authority.LeaseExpiresAt, &request.SessionID, &request.PlanID, &data)
	if err != nil {
		return authority, request, nil, grantError(err)
	}
	var locator playback.ExecutorRecipeLocatorV3
	if err := json.Unmarshal(data, &locator); err != nil {
		return authority, request, nil, err
	}
	if err := locator.Validate(); err != nil {
		return authority, request, nil, err
	}
	return authority, request, &locator, nil
}

// issueCandidateGrant runs only after registration and attempt locks are held.
// The predecessor deadline is frozen in the replacement document and is never
// changed here; only the aggregate deadline used by final stop grows.
func (s *Postgres) issueCandidateGrant(ctx context.Context, tx pgx.Tx, authority playback.AttemptAuthorityV3, request playback.AttemptGrantRequestV3, grant *playback.AttemptGrantV3) error {
	if request.Purpose != playback.AttemptGrantExecuteV3 {
		return pgx.ErrNoRows
	}
	var data []byte
	var key playback.RouteReplacementKeyV3
	err := tx.QueryRow(ctx, `SELECT replan_request_id,request_digest,base_replan_request_id,lease_owner,route_replacement FROM playback_v3_replans WHERE session_id=$1::uuid AND session_id=(SELECT session_id FROM playback_v3_attempts WHERE playback_attempt_id=$2) AND route_replacement->>'phase' IN ('staged','ready','retiring') FOR UPDATE`, request.SessionID, authority.PlaybackAttemptID).Scan(&key.RequestID, &key.Digest, &key.BaseReplanID, &key.LeaseToken, &data)
	if err != nil {
		return err
	}
	if len(data) > routeReplacementLimit {
		return pgx.ErrNoRows
	}
	var doc playback.RouteReplacementV3
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	if doc.Key != key || doc.Next.PlaybackAttemptID != authority.PlaybackAttemptID || doc.Next.SessionID != request.SessionID || doc.Next.CurrentPlanID != request.PlanID || doc.Route.Executor != request.Executor || doc.Route.TransportID != request.TransportID || doc.Route.ExecutionNodeID != request.NodeID {
		return pgx.ErrNoRows
	}
	previousRoute, _ := json.Marshal(doc.PreviousRoute)
	previousLocator, _ := json.Marshal(doc.PreviousLocator)
	return tx.QueryRow(ctx, `WITH timing AS MATERIALIZED (SELECT clock_timestamp() AS now)
 UPDATE playback_v3_attempts SET control_grant_not_after=GREATEST(control_grant_not_after,LEAST(timing.now+$5*interval '1 microsecond',control_lease_expires_at,expires_at)),updated_at=timing.now FROM timing
 WHERE playback_attempt_id=$1 AND control_incarnation=$2::uuid AND control_owner=$3::uuid AND control_epoch=$4
 AND control_state='active' AND control_activation->>'phase'='activated'
 AND control_lease_expires_at>timing.now AND expires_at>timing.now
 AND session_id=$6::uuid AND current_plan_id=$7 AND current_replan_request_id=$8
 AND (($9 IN ('staged','ready') AND control_route=$10::jsonb AND control_recipe_locator=$11::jsonb AND control_retiring_replan IS NULL)
 OR ($9='retiring' AND control_route IS NULL AND control_recipe_locator IS NULL AND control_retiring_replan=$12))
 RETURNING control_state,control_lease_expires_at,timing.now,LEAST(timing.now+$5*interval '1 microsecond',control_lease_expires_at,expires_at)`, authority.PlaybackAttemptID, authority.Incarnation, authority.OwnerID, authority.Epoch, min(request.Duration, s.grantMaxDuration).Microseconds(), request.SessionID, doc.PreviousPlanID, doc.Key.BaseReplanID, doc.Phase, previousRoute, previousLocator, doc.Key.RequestID).Scan(&grant.Authority.State, &grant.Authority.LeaseExpiresAt, &grant.IssuedAt, &grant.NotAfter)
}
