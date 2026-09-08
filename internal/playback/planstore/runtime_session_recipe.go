package planstore

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ResolveCurrentSession discovers only the currently activated route. Bound is
// true even when that attempt is unavailable, so callers never fall back to a
// mutable legacy recipe for a retiring, stopped or expired bound session.
// The returned metadata confers no authority; serving still acquires its grant.
func (s *ExecutorRuntime) ResolveCurrentSession(ctx context.Context, sessionID string) (card *playback.RecipeCard, bound bool, err error) {
	id, err := uuid.Parse(sessionID)
	if err != nil || id == uuid.Nil || id.String() != sessionID {
		return nil, true, playback.ErrSessionNotFound
	}
	var state playback.AttemptAuthorityStateV3
	var active bool
	var routeJSON []byte
	err = s.store.db.QueryRow(ctx, `SELECT control_state,COALESCE(control_activation->>'phase'='activated',false),control_route FROM playback_v3_attempts WHERE session_id=$1::uuid`, sessionID).Scan(&state, &active, &routeJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if state == "legacy" {
		return nil, false, nil
	}
	if state != playback.AttemptActiveV3 || !active || len(routeJSON) == 0 {
		return nil, true, playback.ErrStaleAttemptAuthorityV3
	}
	var route playback.AttemptGrantRouteV3
	if err := json.Unmarshal(routeJSON, &route); err != nil {
		return nil, true, err
	}
	if route.EgressNodeID != s.nodeID {
		return nil, true, playback.ErrStaleAttemptAuthorityV3
	}
	card, err = s.Resolve(ctx, route.TransportID, route.Executor)
	if err != nil || card == nil {
		return nil, true, err
	}
	if card.SessionID != sessionID || card.RoutingEgressNodeID != s.nodeID {
		return nil, true, playback.ErrStaleAttemptAuthorityV3
	}
	return card, true, nil
}
