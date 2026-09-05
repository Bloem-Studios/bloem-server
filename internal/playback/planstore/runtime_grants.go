package planstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// ImmutableExecutorRecipes reads descriptors; this interface grants no authority.
type ImmutableExecutorRecipes interface {
	GetImmutable(context.Context, playback.ExecutorRecipeLocatorV3) (*playback.RecipeCard, error)
}

// ExecutorRuntime binds a configured node to PostgreSQL grant issuance and the
// immutable recipe store. Constructing it does not activate playback routes.
type ExecutorRuntime struct {
	store   *Postgres
	recipes ImmutableExecutorRecipes
	nodeID  int
	clock   playback.RuntimeGrantClockV3
	policy  playback.RuntimeGrantPolicyV3
}

func NewExecutorRuntime(store *Postgres, recipes ImmutableExecutorRecipes, nodeID int, clock playback.RuntimeGrantClockV3, policy playback.RuntimeGrantPolicyV3) (*ExecutorRuntime, error) {
	if store == nil || store.db == nil || store.grantMaxDuration <= 0 || recipes == nil || nodeID < 0 || clock == nil {
		return nil, fmt.Errorf("executor runtime dependencies and grant policy are required")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if policy.MaxDuration > store.grantMaxDuration {
		return nil, fmt.Errorf("runtime duration exceeds database grant policy")
	}
	return &ExecutorRuntime{store: store, recipes: recipes, nodeID: nodeID, clock: clock, policy: policy}, nil
}

// binding only discovers the candidate row. IssueAttemptGrant performs the
// subsequent live CAS; a read cannot authorize execution by itself.
func (s *ExecutorRuntime) binding(ctx context.Context, transportID string, executor playback.ExecutorNamespaceV3) (playback.AttemptAuthorityV3, playback.AttemptGrantRequestV3, error) {
	var authority playback.AttemptAuthorityV3
	request := playback.AttemptGrantRequestV3{Executor: executor, TransportID: transportID, NodeID: s.nodeID, Duration: s.policy.MaxDuration}
	if err := executor.Validate(); err != nil {
		return authority, request, err
	}
	if transportID == "" {
		return authority, request, playback.ErrStaleAttemptAuthorityV3
	}
	encoded, err := json.Marshal(executor)
	if err != nil {
		return authority, request, err
	}
	err = s.store.db.QueryRow(ctx, `SELECT playback_attempt_id,control_incarnation::text,control_owner::text,control_epoch,control_state,control_lease_expires_at,session_id::text,current_plan_id
 FROM playback_v3_attempts WHERE control_incarnation=$1::uuid AND control_epoch=$2
 AND control_route->'executor'=$3::jsonb AND control_route->>'transport_id'=$4
 AND control_state IN ('preparing','active') AND control_lease_expires_at>clock_timestamp() AND expires_at>clock_timestamp()`, executor.Incarnation, executor.Epoch, encoded, transportID).Scan(
		&authority.PlaybackAttemptID, &authority.Incarnation, &authority.OwnerID, &authority.Epoch, &authority.State, &authority.LeaseExpiresAt, &request.SessionID, &request.PlanID)
	return authority, request, grantError(err)
}

// Acquire is suitable for playback.ExecutorGrantProviderV3. The node identity
// comes from deployment configuration, never an untrusted stream request.
func (s *ExecutorRuntime) Acquire(ctx context.Context, transportID string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
	authority, request, err := s.binding(ctx, transportID, executor)
	if err != nil {
		return nil, err
	}
	request.Purpose = purpose
	return playback.AcquireRuntimeGrantV3(ctx, s.store.IssueAttemptGrant, s.clock, s.policy, authority, request)
}

// Resolve is suitable for the worker's explicit recipe resolver. Its result is
// immutable input only; StartTranscode must acquire execute authority afterwards.
func (s *ExecutorRuntime) Resolve(ctx context.Context, transportID string, executor playback.ExecutorNamespaceV3) (*playback.RecipeCard, error) {
	authority, request, err := s.binding(ctx, transportID, executor)
	if err != nil {
		return nil, err
	}
	locator, err := s.store.GetAttemptRecipeLocator(ctx, authority)
	if err != nil {
		return nil, err
	}
	if locator.Executor != executor {
		return nil, playback.ErrStaleAttemptAuthorityV3
	}
	card, err := s.recipes.GetImmutable(ctx, *locator)
	if err != nil {
		return nil, err
	}
	if card == nil || playback.MatchExecutorNamespace(card.Executor, &executor) != nil || card.SessionID != request.SessionID {
		return nil, playback.ErrStaleAttemptAuthorityV3
	}
	cardTransport := card.TranscodeTransportID
	if cardTransport == "" {
		cardTransport = card.SessionID
	}
	if cardTransport != transportID {
		return nil, playback.ErrStaleAttemptAuthorityV3
	}
	return card, nil
}
