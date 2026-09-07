package planstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OpenOutputTransfer permits a single egress response to request output from its
// selected execution node. Only the configured egress runtime can open it. The
// random locator is not a lease: the worker must acquire and renew a separately
// fenced output-transfer grant for every response using it.
func (s *ExecutorRuntime) OpenOutputTransfer(ctx context.Context, transportID string, executor playback.ExecutorNamespaceV3) (string, func(), error) {
	authority, request, err := s.binding(ctx, transportID, executor)
	if err != nil {
		return "", nil, err
	}
	permitID := uuid.NewString()
	encoded, err := json.Marshal(executor)
	if err != nil {
		return "", nil, err
	}
	err = s.store.withAuthorityLock(ctx, authority.PlaybackAttemptID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO playback_output_transfer_permits
		 (permit_id,playback_attempt_id,incarnation,owner_id,epoch,session_id,plan_id,transport_id,executor,execution_node_id,egress_node_id)
		 SELECT $1::uuid,playback_attempt_id,control_incarnation,control_owner,control_epoch,session_id,current_plan_id,
		 control_route->>'transport_id',control_route->'executor',(control_route->>'execution_node_id')::bigint,(control_route->>'egress_node_id')::bigint
		 FROM playback_v3_attempts WHERE playback_attempt_id=$2 AND control_incarnation=$3::uuid AND control_owner=$4::uuid AND control_epoch=$5
		 AND control_state='active' AND control_lease_expires_at>clock_timestamp() AND expires_at>clock_timestamp()
		 AND (control_activation IS NULL OR control_activation->>'phase'='activated')
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
		_, _ = s.store.db.Exec(cleanup, `DELETE FROM playback_output_transfer_permits WHERE permit_id=$1::uuid`, permitID)
	}
	return permitID, closePermit, nil
}

// AcquireOutputTransfer runs on the execution node. Neither an ordinary signed
// media token nor the worker listener's bearer can substitute for the permit.
func (s *ExecutorRuntime) AcquireOutputTransfer(ctx context.Context, transportID string, executor playback.ExecutorNamespaceV3, permitID string) (*playback.RuntimeGrantV3, error) {
	if id, err := uuid.Parse(permitID); err != nil || id == uuid.Nil || id.String() != permitID {
		return nil, playback.ErrStaleAttemptAuthorityV3
	}
	authority, request, err := s.binding(ctx, transportID, executor)
	if err != nil {
		return nil, err
	}
	request.OutputTransferID = permitID
	request.Purpose = playback.AttemptGrantTransferV3
	// This read discovers only the candidate peer. The issuing CAS checks every
	// permit field against the live route while holding the authority lock.
	if err := s.store.db.QueryRow(ctx, `SELECT egress_node_id FROM playback_output_transfer_permits WHERE permit_id=$1::uuid`, permitID).Scan(&request.EgressNodeID); err != nil {
		return nil, grantError(err)
	}
	return playback.AcquireRuntimeGrantV3(ctx, s.store.IssueAttemptGrant, s.clock, s.policy, authority, request)
}
