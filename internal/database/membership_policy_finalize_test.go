package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// beatLegacyNode writes a heartbeat the way a process that predates the
// membership policy protocol does: no capability marker, no instance id. The
// heartbeat trigger records it as a 'legacy' rollout observation.
func beatLegacyNode(t *testing.T, pool *pgxpool.Pool, nodeID string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
INSERT INTO public.node_heartbeats (node_id,node_type,node_url,updated_at)
VALUES ($1,'integrated','http://legacy.test',$2)
ON CONFLICT (node_id) DO UPDATE SET updated_at=EXCLUDED.updated_at`, nodeID, at); err != nil {
		t.Fatalf("legacy heartbeat for %s: %v", nodeID, err)
	}
}

// beatCapableNode writes the heartbeat a membership-policy-aware process writes.
func beatCapableNode(t *testing.T, pool *pgxpool.Pool, nodeID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin capable heartbeat: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.schema_capability_writer','v1',true)`); err != nil {
		t.Fatalf("mark capable heartbeat: %v", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO public.node_heartbeats (
    node_id,node_type,node_url,updated_at,schema_capabilities,instance_id)
VALUES ($1,'integrated','http://capable.test',$2,ARRAY['membership_policy_v1'],$3)
ON CONFLICT (node_id) DO UPDATE SET
    node_url=EXCLUDED.node_url,
    updated_at=EXCLUDED.updated_at,
    schema_capabilities=EXCLUDED.schema_capabilities,
    instance_id=EXCLUDED.instance_id`, nodeID, at, uuid.New()); err != nil {
		t.Fatalf("capable heartbeat: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit capable heartbeat: %v", err)
	}
}

func membershipPolicyPhase(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var phase string
	if err := pool.QueryRow(context.Background(),
		`SELECT phase FROM public.membership_policy_authority WHERE singleton`).Scan(&phase); err != nil {
		t.Fatalf("read authority phase: %v", err)
	}
	return phase
}

func usersHasColumn(t *testing.T, pool *pgxpool.Pool, column string) bool {
	t.Helper()
	var present bool
	if err := pool.QueryRow(context.Background(), `
SELECT EXISTS(SELECT 1 FROM information_schema.columns
WHERE table_schema='public' AND table_name='users' AND column_name=$1)`, column).Scan(&present); err != nil {
		t.Fatalf("inspect users.%s: %v", column, err)
	}
	return present
}

// The compatibility phase is a policy freeze that only finalization lifts, and
// finalization renames the legacy columns out from under anything still reading
// them. So it must refuse while any node is observed on the legacy protocol,
// and must go through cleanly once every node is capable.
func TestFinalizeMembershipPolicyAuthorityLifecycle(t *testing.T) {
	pool := newMembershipPolicyLegacyPool(t)
	ctx := context.Background()
	migrateMembershipPolicyUp(t, pool)

	if got := membershipPolicyPhase(t, pool); got != "compatibility" {
		t.Fatalf("phase = %q, want compatibility", got)
	}

	now := time.Now().UTC().Truncate(time.Second)
	beatLegacyNode(t, pool, "finalize-node-a", now)

	changed, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool)
	if !errors.Is(err, tenancy.ErrMembershipPolicyRolloutIncomplete) {
		t.Fatalf("finalize with a legacy node: err = %v, want ErrMembershipPolicyRolloutIncomplete", err)
	}
	if changed {
		t.Fatal("finalize reported a change while refusing")
	}
	if got := membershipPolicyPhase(t, pool); got != "compatibility" {
		t.Fatalf("phase = %q after a refused finalize, want compatibility", got)
	}
	if !usersHasColumn(t, pool, "access_policy_revision") {
		t.Fatal("a refused finalize renamed the legacy policy columns anyway")
	}

	// The node upgrades and now advertises the capability.
	// The capable heartbeat writes a NEW observation and repoints the node at it,
	// leaving the old legacy row behind. Finalization drains those superseded
	// rows itself, so a plain rolling upgrade needs no operator drain.
	beatCapableNode(t, pool, "finalize-node-a", now.Add(time.Minute))
	var supersededLegacy int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM public.membership_policy_rollout_observations WHERE state='legacy'`).Scan(&supersededLegacy); err != nil {
		t.Fatalf("count legacy observations: %v", err)
	}
	if supersededLegacy == 0 {
		t.Fatal("expected the upgrade to leave a superseded legacy observation to drain")
	}

	changed, err = tenancy.FinalizeMembershipPolicyAuthority(ctx, pool)
	if err != nil {
		t.Fatalf("finalize with every node capable: %v", err)
	}
	if !changed {
		t.Fatal("finalize reported no change while still in the compatibility phase")
	}
	if got := membershipPolicyPhase(t, pool); got != "finalized" {
		t.Fatalf("phase = %q after finalize, want finalized", got)
	}
	var drained int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM public.membership_policy_rollout_observations
WHERE state='drained' AND drained_at IS NOT NULL`).Scan(&drained); err != nil {
		t.Fatalf("count drained observations: %v", err)
	}
	if drained == 0 {
		t.Fatal("the superseded legacy observation was not drained")
	}

	// The legacy projection must be renamed away, not dropped: the migration's
	// Down path restores from rollback_membership_*.
	for _, column := range []string{"access_policy_revision", "max_streams", "access_group_id"} {
		if usersHasColumn(t, pool, column) {
			t.Fatalf("users.%s survived finalization", column)
		}
		if !usersHasColumn(t, pool, "rollback_membership_"+column) {
			t.Fatalf("users.rollback_membership_%s missing after finalization", column)
		}
	}

	// Re-running must be a no-op so a redeploy or a racing replica cannot fail.
	changed, err = tenancy.FinalizeMembershipPolicyAuthority(ctx, pool)
	if err != nil {
		t.Fatalf("second finalize: %v", err)
	}
	if changed {
		t.Fatal("second finalize reported a change")
	}
}
