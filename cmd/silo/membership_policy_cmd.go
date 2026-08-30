package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// runMembershipPolicyCommand drives the policy handoff from public.users to
// public.organization_memberships.
//
// The compatibility phase the migration installs is a deliberate freeze: policy
// writes are fenced on users and frozen on memberships, so no policy edit
// succeeds by any route until finalization runs. Finalization is gated on every
// node reporting the membership_policy_v1 capability, which makes it an
// operator action rather than a migration — and an operator action needs a
// command, which is what this is.
func runMembershipPolicyCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: silo membership-policy {status|finalize}")
	}
	command := args[0]
	flags := flag.NewFlagSet("membership-policy "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	envFile := flags.String("env", ".env", "path to .env bootstrap file")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	bc, err := config.LoadBootstrap(*envFile)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	pool, err := database.NewPool(ctx, config.DatabaseConfig{URL: bc.DatabaseURL, MaxConnections: 4})
	if err != nil {
		return fmt.Errorf("database pool: %w", err)
	}
	defer pool.Close()

	switch command {
	case "status":
		return printMembershipPolicyStatus(ctx, pool)
	case "finalize":
		changed, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool)
		if err != nil {
			if errors.Is(err, tenancy.ErrMembershipPolicyRolloutIncomplete) {
				// Name the blocking nodes rather than a bare failure: the
				// operator's next move is to upgrade or drain exactly those.
				fmt.Println("membership policy handoff blocked; nodes still on the legacy protocol:")
				fmt.Printf("  %v\n", err)
				return err
			}
			return err
		}
		if !changed {
			fmt.Println("membership policy authority is already finalized; nothing to do")
			return nil
		}
		fmt.Println("membership policy authority finalized; organization_memberships is now the policy source")
		return printMembershipPolicyStatus(ctx, pool)
	default:
		return fmt.Errorf("unknown membership-policy command %q", command)
	}
}

// printMembershipPolicyStatus reports the phase plus any node still observed on
// the legacy protocol, which is the only thing that blocks finalization.
func printMembershipPolicyStatus(ctx context.Context, pool *pgxpool.Pool) error {
	var phase string
	var finalizedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT phase, finalized_at FROM public.membership_policy_authority WHERE singleton`,
	).Scan(&phase, &finalizedAt); err != nil {
		return fmt.Errorf("read membership policy authority: %w", err)
	}
	fmt.Printf("phase: %s\n", phase)
	if finalizedAt != nil {
		fmt.Printf("finalized_at: %s\n", finalizedAt.UTC().Format(time.RFC3339))
	}

	rows, err := pool.Query(ctx, `
		SELECT observations.node_id, observations.state, observations.last_seen_at,
		       (heartbeats.node_id IS NULL) AS node_gone
		FROM public.membership_policy_rollout_observations AS observations
		LEFT JOIN public.node_heartbeats AS heartbeats ON heartbeats.node_id = observations.node_id
		ORDER BY observations.state, observations.node_id`)
	if err != nil {
		return fmt.Errorf("read membership policy rollout observations: %w", err)
	}
	defer rows.Close()
	fmt.Println("observations:")
	any := false
	for rows.Next() {
		var nodeID, state string
		var lastSeen time.Time
		var nodeGone bool
		if err := rows.Scan(&nodeID, &state, &lastSeen, &nodeGone); err != nil {
			return fmt.Errorf("scan membership policy rollout observation: %w", err)
		}
		any = true
		suffix := ""
		if nodeGone {
			suffix = " (heartbeat gone)"
		}
		fmt.Printf("  %-24s %-8s last_seen=%s%s\n", nodeID, state, lastSeen.UTC().Format(time.RFC3339), suffix)
	}
	if !any {
		fmt.Println("  (none)")
	}
	return rows.Err()
}
