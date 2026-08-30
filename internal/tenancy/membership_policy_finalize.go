package tenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrMembershipPolicyRolloutIncomplete reports that at least one node is still
// observed on the legacy policy protocol, so the authority cannot hand over yet.
// Drain the node (see membership_policy_rollout_observations, operator_drain_v1)
// or let it heartbeat with the membership_policy_v1 capability first.
var ErrMembershipPolicyRolloutIncomplete = errors.New("tenancy: membership policy rollout incomplete")

// membershipPolicyColumns are the legacy policy columns that public.users
// carries during the compatibility phase. Finalization renames each to
// rollback_membership_<column>, which is the projection the migration's Down
// path expects to find. Keep this list in step with
// 20260829085838_membership_policy_isolation.
var membershipPolicyColumns = []string{
	"access_group_id",
	"permissions",
	"library_ids",
	"max_playback_quality",
	"max_streams",
	"max_transcodes",
	"transcode_allowed",
	"audio_transcode_allowed",
	"download_allowed",
	"download_transcode_allowed",
	"requests_allowed",
	"max_profiles",
	"access_policy_revision",
}

// FinalizeMembershipPolicyAuthority completes the policy handoff from
// public.users to public.organization_memberships.
//
// The compatibility phase is a deliberate freeze: policy writes are fenced on
// users and frozen on organization_memberships, so no policy edit succeeds by
// any route until this runs. It is therefore an operator action gated on
// rollout state rather than a plain migration, and the window it closes should
// be short.
//
// It reports whether it changed anything, so calling it on an already-finalized
// deployment is a no-op rather than an error.
func FinalizeMembershipPolicyAuthority(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	if pool == nil {
		return false, errors.New("tenancy: finalize membership policy requires a database pool")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("tenancy: begin membership policy finalize: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize against heartbeat and drain writers, which take this lock
	// shared. Taking it exclusively means no node can flip between legacy and
	// capable between the readiness check below and the phase transition.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('bloem.membership_policy_handoff', 0))`); err != nil {
		return false, fmt.Errorf("tenancy: lock membership policy handoff: %w", err)
	}

	var phase string
	if err := tx.QueryRow(ctx, `
		SELECT phase FROM public.membership_policy_authority WHERE singleton FOR UPDATE`).Scan(&phase); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, errors.New("tenancy: membership policy authority row is missing")
		}
		return false, fmt.Errorf("tenancy: read membership policy authority: %w", err)
	}
	if phase == "finalized" {
		return false, nil
	}

	// A rolling upgrade leaves the node's old legacy observation behind: the
	// capable heartbeat writes a NEW observation and repoints node_heartbeats at
	// it. Draining those superseded rows is safe and mechanical -- the node has
	// demonstrably upgraded, because its current heartbeat points elsewhere.
	//
	// A legacy observation that is merely STALE is deliberately not drained here:
	// that node may simply be down and could come back still speaking the legacy
	// protocol, so retiring it is an operator judgement, not a side effect of
	// finalizing.
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_observation_writer', 'operator_drain_v1', true)`); err != nil {
		return false, fmt.Errorf("tenancy: mark membership policy drain writer: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE public.membership_policy_rollout_observations AS observations
		SET state = 'drained', drained_at = GREATEST(now(), observations.last_seen_at)
		FROM public.node_heartbeats AS heartbeats
		WHERE heartbeats.node_id = observations.node_id
		  AND observations.state = 'legacy'
		  AND observations.instance_id IS NULL
		  AND heartbeats.membership_policy_rollout_observation_id IS DISTINCT FROM observations.observation_id`); err != nil {
		return false, fmt.Errorf("tenancy: drain superseded legacy observations: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_observation_writer', '', true)`); err != nil {
		return false, fmt.Errorf("tenancy: clear membership policy drain writer: %w", err)
	}

	var legacyNodes []string
	rows, err := tx.Query(ctx, `
		SELECT node_id
		FROM public.membership_policy_rollout_observations
		WHERE state = 'legacy'
		ORDER BY node_id`)
	if err != nil {
		return false, fmt.Errorf("tenancy: read membership policy rollout observations: %w", err)
	}
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			rows.Close()
			return false, fmt.Errorf("tenancy: scan membership policy rollout observation: %w", err)
		}
		legacyNodes = append(legacyNodes, nodeID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("tenancy: read membership policy rollout observations: %w", err)
	}
	if len(legacyNodes) > 0 {
		return false, fmt.Errorf("%w: %s", ErrMembershipPolicyRolloutIncomplete, strings.Join(legacyNodes, ", "))
	}

	// guard_membership_policy_authority_transition accepts the phase change
	// only from a session that claims the finalizer role.
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_finalizer', 'v1', true)`); err != nil {
		return false, fmt.Errorf("tenancy: mark membership policy finalizer: %w", err)
	}

	// The fence reads the very columns renamed below, so it has to go first.
	if _, err := tx.Exec(ctx, `DROP TRIGGER IF EXISTS users_membership_policy_authority_fence ON public.users`); err != nil {
		return false, fmt.Errorf("tenancy: drop legacy users policy fence: %w", err)
	}

	for _, column := range membershipPolicyColumns {
		statement := fmt.Sprintf(
			`ALTER TABLE public.users RENAME COLUMN %s TO %s`,
			quoteIdentifier(column),
			quoteIdentifier("rollback_membership_"+column),
		)
		if _, err := tx.Exec(ctx, statement); err != nil {
			return false, fmt.Errorf("tenancy: rename legacy policy column %s: %w", column, err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE public.membership_policy_authority
		SET phase = 'finalized', finalized_at = now()
		WHERE singleton`); err != nil {
		return false, fmt.Errorf("tenancy: finalize membership policy authority: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("tenancy: commit membership policy finalize: %w", err)
	}
	return true, nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
