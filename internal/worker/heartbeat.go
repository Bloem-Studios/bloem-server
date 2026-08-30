package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodeidentity"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type heartbeatStore interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// HeartbeatWriter periodically upserts a row in node_heartbeats to signal
// this node is alive. All node types (integrated, api, proxy, transcode)
// should run a HeartbeatWriter.
type HeartbeatWriter struct {
	store heartbeatStore
	// instanceID identifies THIS process. The rollout observations key a capable
	// node by (node_id, instance_id) so a restart is a new observation rather
	// than a silent reuse of the old one.
	instanceID string
	nodeID     string
	nodeType   string
	nodeURL    string
	interval   time.Duration

	lifecycleCtx context.Context
	cancel       context.CancelFunc
	startOnce    sync.Once
	stopOnce     sync.Once
	done         chan struct{}
}

// NewHeartbeatWriter creates a HeartbeatWriter for the given node identity.
func NewHeartbeatWriter(pool *pgxpool.Pool, nodeID, nodeType, nodeURL string) *HeartbeatWriter {
	return newHeartbeatWriter(pool, nodeID, nodeType, nodeURL)
}

func newHeartbeatWriter(store heartbeatStore, nodeID, nodeType, nodeURL string) *HeartbeatWriter {
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	return &HeartbeatWriter{
		store:        store,
		instanceID:   nodeidentity.InstanceID(),
		nodeID:       nodeID,
		nodeType:     nodeType,
		nodeURL:      nodeURL,
		interval:     15 * time.Second,
		lifecycleCtx: lifecycleCtx,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
}

// Beat performs a single heartbeat upsert.
func (hw *HeartbeatWriter) Beat(ctx context.Context) error {
	// Once the membership policy authority is finalized,
	// register_membership_policy_heartbeat rejects any heartbeat that does not
	// declare the membership_policy_v1 capability, because a node that still
	// speaks the legacy protocol must not silently pass for a capable one.
	//
	// The marker is transaction-local (SET LOCAL), and this store only exposes
	// Exec, so it rides in the statement itself: the WHERE is evaluated while
	// producing the row, which is before the trigger fires, and a lone statement
	// is its own transaction.
	_, err := hw.store.Exec(ctx, `
		INSERT INTO node_heartbeats (node_id, node_type, node_url, updated_at, schema_capabilities, instance_id)
		SELECT $1, $2, $3, NOW(), ARRAY['membership_policy_v1'], $4::uuid
		WHERE set_config('bloem.schema_capability_writer', 'v1', true) IS NOT NULL
		ON CONFLICT (node_id) DO UPDATE SET
			node_type           = EXCLUDED.node_type,
			node_url            = EXCLUDED.node_url,
			updated_at          = NOW(),
			schema_capabilities = EXCLUDED.schema_capabilities,
			instance_id         = EXCLUDED.instance_id
	`, hw.nodeID, hw.nodeType, hw.nodeURL, hw.instanceID)
	if err != nil {
		return fmt.Errorf("heartbeat upsert: %w", err)
	}
	return nil
}

// Start begins the background heartbeat loop exactly once. Runs until Stop is
// called.
func (hw *HeartbeatWriter) Start() {
	hw.startOnce.Do(func() { go hw.run() })
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

// Stop signals the heartbeat loop to stop. It is safe to call repeatedly. Use
// StopAndWait when later work must not race with an in-flight heartbeat.
func (hw *HeartbeatWriter) Stop() {
	hw.stopOnce.Do(func() {
		hw.cancel()
		// If Start has not claimed the lifecycle, claim and complete it here so
		// StopAndWait also works before Start and future Start calls are harmless.
		hw.startOnce.Do(func() { close(hw.done) })
	})
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

// CleanupSelf removes this node's heartbeat row and all its sessions from
// playback_sessions_sync. Call during graceful shutdown.
func (hw *HeartbeatWriter) CleanupSelf(ctx context.Context) error {
	_, err := hw.store.Exec(ctx, `
		DELETE FROM playback_sessions_sync WHERE reporting_node = $1
	`, hw.nodeID)
	if err != nil {
		return fmt.Errorf("deleting sessions for node %s: %w", hw.nodeID, err)
	}

	// A heartbeat may only be deleted by a session that names the exact node and
	// instance it is retiring, so a sweep cannot blindly drop another node's row.
	// This node knows both. The markers are transaction-local and this store
	// only exposes Exec, so they ride in the statement itself.
	_, err = hw.store.Exec(ctx, `
		DELETE FROM node_heartbeats
		WHERE node_id = $1
		  AND set_config('bloem.heartbeat_cleanup_writer', 'v1', true) IS NOT NULL
		  AND set_config('bloem.heartbeat_cleanup_node_id', $1, true) IS NOT NULL
		  AND set_config('bloem.heartbeat_cleanup_instance_id', $2, true) IS NOT NULL
	`, hw.nodeID, hw.instanceID)
	if err != nil {
		return fmt.Errorf("deleting heartbeat for node %s: %w", hw.nodeID, err)
	}
	return nil
}
