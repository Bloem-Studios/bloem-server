package planstore

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/jackc/pgx/v5"
)

var _ playback.BoundRouteReplacementStoreV3 = (*Postgres)(nil)

const routeReplacementLimit = 256 * 1024

func replacementConflict() error { return playback.ErrReplanSupersededV3 }

// withReplacement preserves registration -> attempt -> replan lock order. Read
// remains possible during terminal drain or owner expiry for exact cleanup;
// mutation still requires the original live active authority and admission.
func (s *Postgres) withReplacement(ctx context.Context, binding playback.InitialActivationBindingV3, key playback.RouteReplacementKeyV3, mutate bool, fn func(pgx.Tx, *initialActivationRow, *playback.RouteReplacementV3) error) (playback.RouteReplacementV3, error) {
	var result playback.RouteReplacementV3
	if key.RequestID == "" || key.Digest == "" || key.LeaseToken == "" {
		return result, replacementConflict()
	}
	_, err := s.withInitialActivation(ctx, binding, func(tx pgx.Tx, row *initialActivationRow) (playback.InitialActivationV3, error) {
		if row.activation == nil {
			return playback.InitialActivationV3{}, replacementConflict()
		}
		if mutate && (!row.admitting || !row.live() || row.authority.State != playback.AttemptActiveV3 || row.activation.Phase != playback.InitialActivationActivatedV3) {
			return *row.activation, replacementConflict()
		}
		var digest, base, lease, state string
		var until time.Time
		var data []byte
		err := tx.QueryRow(ctx, `SELECT request_digest,base_replan_request_id,lease_owner,state,lease_expires_at,route_replacement FROM playback_v3_replans WHERE session_id=$1::uuid AND replan_request_id=$2 FOR UPDATE`, binding.Scope.SessionID, key.RequestID).Scan(&digest, &base, &lease, &state, &until, &data)
		if err != nil {
			return *row.activation, grantError(err)
		}
		// Replan acquisition may wait behind another transaction. Re-sample
		// authority time only after all three locks have been acquired.
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&row.now); err != nil {
			return *row.activation, err
		}
		if mutate && !row.live() {
			return *row.activation, replacementConflict()
		}
		if digest != key.Digest || base != key.BaseReplanID || lease != key.LeaseToken {
			return *row.activation, replacementConflict()
		}
		if len(data) > routeReplacementLimit {
			return *row.activation, replacementConflict()
		}
		var doc *playback.RouteReplacementV3
		if len(data) > 0 {
			doc = new(playback.RouteReplacementV3)
			if err := json.Unmarshal(data, doc); err != nil {
				return *row.activation, err
			}
			if doc.Key != key {
				return *row.activation, replacementConflict()
			}
		} else if state != "active" || !until.After(row.now) {
			return *row.activation, replacementConflict()
		}
		if err := fn(tx, row, doc); err != nil {
			return *row.activation, err
		}
		// The callback saves a document only when it changes; re-read gives the
		// exact persisted form, including PostgreSQL timestamp/JSON normalization.
		if err := tx.QueryRow(ctx, `SELECT route_replacement FROM playback_v3_replans WHERE session_id=$1::uuid AND replan_request_id=$2`, binding.Scope.SessionID, key.RequestID).Scan(&data); err != nil {
			return *row.activation, err
		}
		if len(data) == 0 {
			return *row.activation, replacementConflict()
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return *row.activation, err
		}
		return *row.activation, nil
	})
	return result, err
}

func saveReplacement(ctx context.Context, tx pgx.Tx, session string, doc playback.RouteReplacementV3) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if len(data) > routeReplacementLimit {
		return fmt.Errorf("route replacement exceeds document limit")
	}
	_, err = tx.Exec(ctx, `UPDATE playback_v3_replans SET route_replacement=$3,updated_at=clock_timestamp() WHERE session_id=$1::uuid AND replan_request_id=$2`, session, doc.Key.RequestID, data)
	return err
}

func (s *Postgres) StageBoundRouteReplacement(ctx context.Context, binding playback.InitialActivationBindingV3, next playback.RouteReplacementV3) (playback.RouteReplacementV3, error) {
	r := next.Next
	if next.Phase != "" || next.Ready != nil || !next.DrainNotBefore.IsZero() {
		return playback.RouteReplacementV3{}, replacementConflict()
	}
	if r.PlaybackAttemptID != binding.Fence.AttemptID || r.SessionID != binding.Scope.SessionID || r.UserID != binding.Source.AccountID || r.ProfileID != binding.Scope.ProfileID || r.CurrentReplanRequestID != next.Key.RequestID {
		return playback.RouteReplacementV3{}, replacementConflict()
	}
	if r.CurrentPlanID == "" || r.CurrentPlanID == next.PreviousPlanID || r.CurrentPlan.PlanID != r.CurrentPlanID || r.CurrentPlan.SessionID != r.SessionID || r.CurrentPlan.RequestedMediaFileID != r.RequestedMediaFileID || r.CurrentPlan.EffectiveMediaFileID != r.EffectiveMediaFileID || !r.FrozenRecipe.ValidFor(r.CurrentPlan) {
		return playback.RouteReplacementV3{}, replacementConflict()
	}
	if next.Route.TransportID == "" || next.Route.TransportID == next.PreviousRoute.TransportID || next.Route.ExecutionNodeID < 0 || next.Route.EgressNodeID < 0 || next.Route.Executor == next.PreviousRoute.Executor || next.Locator.Executor != next.Route.Executor || next.PreviousLocator.Executor != next.PreviousRoute.Executor || next.Route.Executor.Incarnation != binding.Fence.Incarnation || next.Route.Executor.Epoch != binding.Fence.Epoch {
		return playback.RouteReplacementV3{}, replacementConflict()
	}
	if err := next.Locator.Validate(); err != nil {
		return playback.RouteReplacementV3{}, err
	}
	if err := next.PreviousLocator.Validate(); err != nil {
		return playback.RouteReplacementV3{}, err
	}
	encodedResponse, err := json.Marshal(r.StartResponse)
	if err != nil {
		return playback.RouteReplacementV3{}, err
	}
	var responseDocument, storedDocument any
	if json.Unmarshal(next.Response, &responseDocument) != nil || json.Unmarshal(encodedResponse, &storedDocument) != nil || !reflect.DeepEqual(responseDocument, storedDocument) {
		return playback.RouteReplacementV3{}, replacementConflict()
	}
	var response playback.DecisionResponseV3
	if json.Unmarshal(next.Response, &response) != nil || !reflect.DeepEqual(response, r.StartResponse) || response.PlaybackPlan == nil || !reflect.DeepEqual(*response.PlaybackPlan, r.CurrentPlan) || response.SessionID != r.SessionID {
		return playback.RouteReplacementV3{}, replacementConflict()
	}
	next.Phase = playback.RouteReplacementStagedV3
	return s.withReplacement(ctx, binding, next.Key, true, func(tx pgx.Tx, row *initialActivationRow, existing *playback.RouteReplacementV3) error {
		if existing != nil {
			frozen := *existing
			frozen.Phase = next.Phase
			frozen.Ready = nil
			frozen.DrainNotBefore = time.Time{}
			// JSONB may reorder the response object; compare decoded structures.
			a, _ := json.Marshal(frozen)
			b, _ := json.Marshal(next)
			var av, bv any
			_ = json.Unmarshal(a, &av)
			_ = json.Unmarshal(b, &bv)
			if !reflect.DeepEqual(av, bv) {
				return replacementConflict()
			}
			return nil
		}
		var matches bool
		route, _ := json.Marshal(next.PreviousRoute)
		locator, _ := json.Marshal(next.PreviousLocator)
		normalized, _ := json.Marshal(r.NormalizedRequest)
		// Preserve any captured client timeline without changing the initial
		// activation. The optional JSON fields keep this storage packet usable
		// before the additive timeline types are joined by the coordinator.
		err := tx.QueryRow(ctx, `SELECT
		 current_plan_id=$2 AND current_replan_request_id=$3
		 AND control_route=$4::jsonb AND control_recipe_locator=$5::jsonb
		 AND requested_media_file_id=$6 AND request_digest=$7
		 AND (normalized_request->>'progress_persistence') IS NOT DISTINCT FROM ($8::jsonb->>'progress_persistence')
		 AND (normalized_request->>'timeline_id') IS NOT DISTINCT FROM ($8::jsonb->>'timeline_id')
		 AND ($9::jsonb->'progress_timeline') IS NOT DISTINCT FROM (control_activation->'binding'->'client_timeline')
		 AND (control_activation->'binding'->'client_timeline' IS NULL OR (control_activation->'binding'->'client_timeline'->>'file_id')::integer=$10)
		 FROM playback_v3_attempts WHERE playback_attempt_id=$1`, binding.Fence.AttemptID, next.PreviousPlanID, next.Key.BaseReplanID, route, locator, r.RequestedMediaFileID, r.RequestDigest, normalized, encodedResponse, r.EffectiveMediaFileID).Scan(&matches)
		if err != nil {
			return err
		}
		if !matches {
			return replacementConflict()
		}
		var pending bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_v3_replans WHERE session_id=$1::uuid AND route_replacement->>'phase' IN ('staged','ready','retiring'))`, binding.Scope.SessionID).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return replacementConflict()
		}
		return saveReplacement(ctx, tx, binding.Scope.SessionID, next)
	})
}

func (s *Postgres) ReadBoundRouteReplacement(ctx context.Context, binding playback.InitialActivationBindingV3, key playback.RouteReplacementKeyV3) (playback.RouteReplacementV3, error) {
	return s.withReplacement(ctx, binding, key, false, func(_ pgx.Tx, _ *initialActivationRow, doc *playback.RouteReplacementV3) error {
		if doc == nil {
			return replacementConflict()
		}
		return nil
	})
}

func (s *Postgres) AcknowledgeBoundRouteReplacement(ctx context.Context, binding playback.InitialActivationBindingV3, key playback.RouteReplacementKeyV3, ready playback.RouteReplacementReadyReceiptV3) (playback.RouteReplacementV3, error) {
	return s.withReplacement(ctx, binding, key, true, func(tx pgx.Tx, _ *initialActivationRow, doc *playback.RouteReplacementV3) error {
		if doc == nil || ready.ReceiptID == "" || len(ready.ReceiptID) > 256 || ready.Route != doc.Route || ready.Locator != doc.Locator {
			return replacementConflict()
		}
		if doc.Ready != nil {
			if *doc.Ready != ready {
				return replacementConflict()
			}
			return nil
		}
		if doc.Phase != playback.RouteReplacementStagedV3 {
			return replacementConflict()
		}
		doc.Ready = &ready
		doc.Phase = playback.RouteReplacementReadyV3
		return saveReplacement(ctx, tx, binding.Scope.SessionID, *doc)
	})
}

func (s *Postgres) BeginBoundRouteRetirement(ctx context.Context, binding playback.InitialActivationBindingV3, key playback.RouteReplacementKeyV3) (playback.RouteReplacementV3, error) {
	return s.withReplacement(ctx, binding, key, true, func(tx pgx.Tx, row *initialActivationRow, doc *playback.RouteReplacementV3) error {
		if doc == nil {
			return replacementConflict()
		}
		if doc.Phase == playback.RouteReplacementRetiringV3 || doc.Phase == playback.RouteReplacementCommittedV3 {
			return nil
		}
		if doc.Phase != playback.RouteReplacementReadyV3 {
			return replacementConflict()
		}
		route, _ := json.Marshal(doc.PreviousRoute)
		locator, _ := json.Marshal(doc.PreviousLocator)
		tag, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET control_route=NULL,control_recipe_locator=NULL,control_retiring_replan=$6,updated_at=clock_timestamp() WHERE playback_attempt_id=$1 AND current_plan_id=$2 AND current_replan_request_id=$3 AND control_route=$4::jsonb AND control_recipe_locator=$5::jsonb`, binding.Fence.AttemptID, doc.PreviousPlanID, key.BaseReplanID, route, locator, key.RequestID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return replacementConflict()
		}
		doc.DrainNotBefore = row.now
		if row.grantNotAfter != nil && row.grantNotAfter.After(doc.DrainNotBefore) {
			doc.DrainNotBefore = *row.grantNotAfter
		}
		doc.Phase = playback.RouteReplacementRetiringV3
		return saveReplacement(ctx, tx, binding.Scope.SessionID, *doc)
	})
}

func (s *Postgres) CompleteBoundRouteReplacement(ctx context.Context, binding playback.InitialActivationBindingV3, key playback.RouteReplacementKeyV3) (playback.RouteReplacementV3, error) {
	return s.withReplacement(ctx, binding, key, true, func(tx pgx.Tx, row *initialActivationRow, doc *playback.RouteReplacementV3) error {
		if doc == nil {
			return replacementConflict()
		}
		if doc.Phase == playback.RouteReplacementCommittedV3 {
			var current bool
			err := tx.QueryRow(ctx, `SELECT current_replan_request_id=$2 AND current_plan_id=$3 FROM playback_v3_attempts WHERE playback_attempt_id=$1`, binding.Fence.AttemptID, key.RequestID, doc.Next.CurrentPlanID).Scan(&current)
			if err != nil {
				return err
			}
			if !current {
				return replacementConflict()
			}
			return nil
		}
		if doc.Phase != playback.RouteReplacementRetiringV3 || doc.DrainNotBefore.After(row.now) {
			return replacementConflict()
		}
		r := doc.Next
		plan, _ := json.Marshal(r.CurrentPlan)
		recipe, _ := json.Marshal(r.FrozenRecipe)
		request, _ := json.Marshal(r.NormalizedRequest)
		response, _ := json.Marshal(r.StartResponse)
		route, _ := json.Marshal(doc.Route)
		locator, _ := json.Marshal(doc.Locator)
		tag, err := tx.Exec(ctx, `UPDATE playback_v3_attempts SET effective_media_file_id=$2,current_plan_id=$3,current_replan_request_id=$4,current_plan=$5,frozen_recipe=$6,normalized_request=$7,start_response=$8,control_route=$9,control_recipe_locator=$10,control_retiring_replan=NULL,updated_at=clock_timestamp() WHERE playback_attempt_id=$1 AND current_plan_id=$11 AND current_replan_request_id=$12 AND control_route IS NULL AND control_recipe_locator IS NULL AND control_retiring_replan=$4`, binding.Fence.AttemptID, r.EffectiveMediaFileID, r.CurrentPlanID, key.RequestID, plan, recipe, request, response, route, locator, doc.PreviousPlanID, key.BaseReplanID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return replacementConflict()
		}
		doc.Phase = playback.RouteReplacementCommittedV3
		if err := saveReplacement(ctx, tx, binding.Scope.SessionID, *doc); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE playback_v3_replans SET state='completed',response=$3 WHERE session_id=$1::uuid AND replan_request_id=$2`, binding.Scope.SessionID, key.RequestID, doc.Response)
		return err
	})
}

func (s *Postgres) CancelBoundRouteReplacement(ctx context.Context, binding playback.InitialActivationBindingV3, key playback.RouteReplacementKeyV3) (playback.RouteReplacementV3, error) {
	return s.withReplacement(ctx, binding, key, true, func(tx pgx.Tx, row *initialActivationRow, doc *playback.RouteReplacementV3) error {
		if doc == nil {
			return replacementConflict()
		}
		if doc.Phase == playback.RouteReplacementCancelledV3 {
			return nil
		}
		if doc.Phase != playback.RouteReplacementStagedV3 && doc.Phase != playback.RouteReplacementReadyV3 {
			return replacementConflict()
		}
		doc.Phase = playback.RouteReplacementCancelledV3
		doc.DrainNotBefore = row.now
		if row.grantNotAfter != nil && row.grantNotAfter.After(doc.DrainNotBefore) {
			doc.DrainNotBefore = *row.grantNotAfter
		}
		return saveReplacement(ctx, tx, binding.Scope.SessionID, *doc)
	})
}
