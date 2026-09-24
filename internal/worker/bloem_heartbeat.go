package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodeidentity"
	"github.com/jackc/pgx/v5/pgconn"
)

type heartbeatStore interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// bloemHB is the Bloem heartbeat lifecycle embedded in HeartbeatWriter.
// It replaces the upstream stop channel with a cancellable lifecycle so
// Stop cancels an in-flight beat, Start/Stop are idempotent, and
// StopAndWait can wait for the single loop to finish.
type bloemHB struct {
	// instanceID identifies THIS process. The rollout observations key a capable
	// node by (node_id, instance_id) so a restart is a new observation rather
	// than a silent reuse of the old one.
	instanceID string

	lifecycleCtx context.Context
	cancel       context.CancelFunc
	startOnce    sync.Once
	stopOnce     sync.Once
	done         chan struct{}
}

func newBloemHB() bloemHB {
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	return bloemHB{
		instanceID:   nodeidentity.InstanceID(),
		lifecycleCtx: lifecycleCtx,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
}

func newHeartbeatWriter(store heartbeatStore, nodeID, nodeType, nodeURL string) *HeartbeatWriter {
	hw := NewHeartbeatWriter(nil, nodeID, nodeType, nodeURL)
	hw.pool = store
	return hw
}

// bloemHeartbeatUpsertSQL is the heartbeat upsert.
//
// Once the membership policy authority is finalized,
// register_membership_policy_heartbeat rejects any heartbeat that does not
// declare the membership_policy_v1 capability, because a node that still
// speaks the legacy protocol must not silently pass for a capable one.
//
// The marker is transaction-local (SET LOCAL), and this store only exposes
// Exec, so it rides in the statement itself: the WHERE is evaluated while
// producing the row, which is before the trigger fires, and a lone statement
// is its own transaction.
const bloemHeartbeatUpsertSQL = `
		INSERT INTO node_heartbeats (node_id, node_type, node_url, updated_at, schema_capabilities, instance_id)
		SELECT $1, $2, $3, NOW(), ARRAY['membership_policy_v1'], $4::uuid
		WHERE set_config('bloem.schema_capability_writer', 'v1', true) IS NOT NULL
		ON CONFLICT (node_id) DO UPDATE SET
			node_type           = EXCLUDED.node_type,
			node_url            = EXCLUDED.node_url,
			updated_at          = NOW(),
			schema_capabilities = EXCLUDED.schema_capabilities,
			instance_id         = EXCLUDED.instance_id
	`

// bloemHeartbeatCleanupSQL deletes this node's heartbeat row.
//
// A heartbeat may only be deleted by a session that names the exact node and
// instance it is retiring, so a sweep cannot blindly drop another node's row.
// This node knows both. The markers are transaction-local and this store
// only exposes Exec, so they ride in the statement itself.
const bloemHeartbeatCleanupSQL = `
		DELETE FROM node_heartbeats
		WHERE node_id = $1
		  AND set_config('bloem.heartbeat_cleanup_writer', 'v1', true) IS NOT NULL
		  AND set_config('bloem.heartbeat_cleanup_node_id', $1, true) IS NOT NULL
		  AND set_config('bloem.heartbeat_cleanup_instance_id', $2, true) IS NOT NULL
	`

// bloemStart begins the background heartbeat loop exactly once. It reports
// false only for a writer built without the Bloem lifecycle, in which case
// Start falls back to the upstream loop.
func (hw *HeartbeatWriter) bloemStart() bool {
	if hw.done == nil {
		return false
	}
	hw.startOnce.Do(func() { go hw.run() })
	return true
}

// bloemStop signals the heartbeat loop to stop. It is safe to call
// repeatedly. Use StopAndWait when later work must not race with an
// in-flight heartbeat.
func (hw *HeartbeatWriter) bloemStop() bool {
	if hw.done == nil {
		return false
	}
	hw.stopOnce.Do(func() {
		hw.cancel()
		// If Start has not claimed the lifecycle, claim and complete it here so
		// StopAndWait also works before Start and future Start calls are harmless.
		hw.startOnce.Do(func() { close(hw.done) })
	})
	return true
}

func (hw *HeartbeatWriter) run() {
	defer close(hw.done)
	if hw.lifecycleCtx.Err() != nil {
		return
	}
	hw.beatWithTimeout("initial")

	ticker := time.NewTicker(hw.interval)
	defer ticker.Stop()
	for {
		select {
		case <-hw.lifecycleCtx.Done():
			return
		case <-ticker.C:
			if hw.lifecycleCtx.Err() != nil {
				return
			}
			hw.beatWithTimeout("periodic")
		}
	}
}

func (hw *HeartbeatWriter) beatWithTimeout(phase string) {
	ctx, cancel := context.WithTimeout(hw.lifecycleCtx, 5*time.Second)
	defer cancel()
	if err := hw.Beat(ctx); err != nil {
		slog.ErrorContext(ctx, "heartbeat failed", "phase", phase, "error", err, "node", hw.nodeID)
	}
}

// StopAndWait cancels the heartbeat lifecycle and waits for its single loop to
// finish. A wait-context error does not consume completion; callers may wait
// again with a fresh context.
func (hw *HeartbeatWriter) StopAndWait(ctx context.Context) error {
	hw.Stop()
	select {
	case <-hw.done:
		return nil
	default:
	}

	select {
	case <-hw.done:
		return nil
	case <-ctx.Done():
		select {
		case <-hw.done:
			return nil
		default:
			return ctx.Err()
		}
	}
}
