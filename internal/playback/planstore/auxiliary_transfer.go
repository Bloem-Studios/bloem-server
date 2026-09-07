package planstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OpenAuxiliaryTransfer creates a response permit for API production. Only the
// configured selected proxy may open it; the API still needs a live grant.
func (s *ExecutorRuntime) OpenAuxiliaryTransfer(ctx context.Context, transportID string, executor playback.ExecutorNamespaceV3) (string, func(), error) {
	authority, request, err := s.binding(ctx, transportID, executor)
	if err != nil {
		return "", nil, err
	}
	if s.nodeID <= 0 {
		return "", nil, playback.ErrStaleAttemptAuthorityV3
	}
	permitID := uuid.NewString()
	encoded, err := json.Marshal(executor)
	if err != nil {
		return "", nil, err
	}
	err = s.store.withGrantAuthority(ctx, authority, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO playback_auxiliary_transfer_permits
		 (permit_id,playback_attempt_id,incarnation,owner_id,epoch,session_id,plan_id,transport_id,executor,egress_node_id)
		 SELECT $1::uuid,playback_attempt_id,control_incarnation,control_owner,control_epoch,session_id,current_plan_id,
		 control_route->>'transport_id',control_route->'executor',(control_route->>'egress_node_id')::bigint
		 FROM playback_v3_attempts WHERE playback_attempt_id=$2 AND control_incarnation=$3::uuid AND control_owner=$4::uuid AND control_epoch=$5
		 AND control_state='active' AND control_lease_expires_at>clock_timestamp() AND expires_at>clock_timestamp()
		 AND control_activation->>'phase'='activated'
		 AND session_id=$6::uuid AND current_plan_id=$7 AND control_route->>'transport_id'=$8
		 AND control_route->'executor'=$9::jsonb AND (control_route->>'egress_node_id')::bigint=$10`,
			permitID, authority.PlaybackAttemptID, authority.Incarnation, authority.OwnerID, authority.Epoch,
			request.SessionID, request.PlanID, transportID, encoded, s.nodeID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return playback.ErrStaleAttemptAuthorityV3
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	closePermit := func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		// A failed cleanup may leave only a locator until attempt retention reaps
		// it. Live grant issuance and the common durable drain remain mandatory.
		_ = s.store.withAuthorityLock(cleanup, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
			_, err := tx.Exec(cleanup, `DELETE FROM playback_auxiliary_transfer_permits WHERE permit_id=$1::uuid AND playback_attempt_id=$2`, permitID, authority.PlaybackAttemptID)
			return err
		})
	}
	return permitID, closePermit, nil
}

// AcquireAuxiliaryTransfer runs only on the API producer (node zero). The
// signed recipe token cannot substitute for the selected proxy's permit.
func (s *ExecutorRuntime) AcquireAuxiliaryTransfer(ctx context.Context, transportID string, executor playback.ExecutorNamespaceV3, permitID string) (*playback.RuntimeGrantV3, error) {
	if s.nodeID != 0 {
		return nil, playback.ErrStaleAttemptAuthorityV3
	}
	if id, err := uuid.Parse(permitID); err != nil || id == uuid.Nil || id.String() != permitID {
		return nil, playback.ErrStaleAttemptAuthorityV3
	}
	authority, request, err := s.binding(ctx, transportID, executor)
	if err != nil {
		return nil, err
	}
	request.AuxiliaryTransferID = permitID
	request.Purpose = playback.AttemptGrantAuxiliaryV3
	// This read discovers only the candidate peer. The issuing CAS checks every
	// permit field against the live route while holding the authority lock.
	if err := s.store.db.QueryRow(ctx, `SELECT egress_node_id FROM playback_auxiliary_transfer_permits WHERE permit_id=$1::uuid`, permitID).Scan(&request.EgressNodeID); err != nil {
		return nil, grantError(err)
	}
	return playback.AcquireRuntimeGrantV3(ctx, s.store.IssueAttemptGrant, s.clock, s.policy, authority, request)
}

func validStoredGrantRequest(r playback.AttemptGrantRequestV3) bool {
	if r.Purpose == playback.AttemptGrantAuxiliaryV3 {
		return r.NodeID == 0 && r.EgressNodeID > 0 && r.AuxiliaryTransferID != "" && r.OutputTransferID == ""
	}
	if r.AuxiliaryTransferID != "" {
		return false
	}
	if r.Purpose == playback.AttemptGrantTransferV3 {
		return r.OutputTransferID != "" && r.EgressNodeID >= 0
	}
	return (r.Purpose == playback.AttemptGrantExecuteV3 || r.Purpose == playback.AttemptGrantServeV3) && r.OutputTransferID == "" && r.EgressNodeID == 0
}

func (s *Postgres) issueAuxiliaryGrant(ctx context.Context, tx pgx.Tx, a playback.AttemptAuthorityV3, r playback.AttemptGrantRequestV3, g *playback.AttemptGrantV3) error {
	executor, err := json.Marshal(r.Executor)
	if err != nil {
		return err
	}
	return tx.QueryRow(ctx, `WITH timing AS MATERIALIZED (SELECT clock_timestamp() AS now)
 UPDATE playback_v3_attempts SET control_grant_not_after=GREATEST(control_grant_not_after,LEAST(timing.now+$5*interval '1 microsecond',control_lease_expires_at,expires_at)),updated_at=timing.now FROM timing
 WHERE playback_attempt_id=$1 AND control_incarnation=$2::uuid AND control_owner=$3::uuid AND control_epoch=$4
 AND control_state='active' AND control_activation->>'phase'='activated' AND control_lease_expires_at>timing.now AND expires_at>timing.now
 AND session_id=$6::uuid AND current_plan_id=$7 AND control_route->>'transport_id'=$8 AND control_route->'executor'=$9::jsonb
 AND (control_route->>'egress_node_id')::bigint=$10
 AND EXISTS (SELECT 1 FROM playback_auxiliary_transfer_permits p WHERE p.permit_id=$11::uuid AND p.playback_attempt_id=$1
 AND p.incarnation=$2::uuid AND p.owner_id=$3::uuid AND p.epoch=$4 AND p.session_id=$6::uuid AND p.plan_id=$7 AND p.transport_id=$8 AND p.executor=$9::jsonb AND p.egress_node_id=$10)
 RETURNING control_state,control_lease_expires_at,timing.now,LEAST(timing.now+$5*interval '1 microsecond',control_lease_expires_at,expires_at)`, a.PlaybackAttemptID, a.Incarnation, a.OwnerID, a.Epoch, min(r.Duration, s.grantMaxDuration).Microseconds(), r.SessionID, r.PlanID, r.TransportID, executor, r.EgressNodeID, r.AuxiliaryTransferID).Scan(&g.Authority.State, &g.Authority.LeaseExpiresAt, &g.IssuedAt, &g.NotAfter)
}

func (s *ExecutorRuntime) ResolveAuxiliary(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3) (playback.AuxiliaryRecipeV3, error) {
	var result playback.AuxiliaryRecipeV3
	if s.nodeID != 0 {
		return result, playback.ErrStaleAttemptAuthorityV3
	}
	a, r, err := s.binding(ctx, transport, executor)
	if err != nil {
		return result, err
	}
	result.Card, err = s.Resolve(ctx, transport, executor)
	if err != nil {
		return playback.AuxiliaryRecipeV3{}, err
	}
	encoded, _ := json.Marshal(executor)
	err = s.store.db.QueryRow(ctx, `SELECT requested_media_file_id FROM playback_v3_attempts WHERE playback_attempt_id=$1 AND control_incarnation=$2::uuid AND control_owner=$3::uuid AND control_epoch=$4 AND current_plan_id=$5 AND control_route->'executor'=$6::jsonb AND control_route->>'transport_id'=$7 AND control_state='active' AND control_lease_expires_at>clock_timestamp() AND expires_at>clock_timestamp()`, a.PlaybackAttemptID, a.Incarnation, a.OwnerID, a.Epoch, r.PlanID, encoded, transport).Scan(&result.RequestedMediaFileID)
	return result, grantError(err)
}
