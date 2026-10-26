package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/nodeidentity"
)

type heartbeatStore interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// bloemHB is the Bloem heartbeat lifecycle embedded in HeartbeatWriter.
// It replaces the upstream stop channel with a cancellable lifecycle so
// Stop cancels an in-flight beat, Start/Stop are idempotent, and
// StopAndWait can wait for the single loop to finish.
type bloemHB struct {
	// instanceID identifies THIS process, so a restart is told apart from the
	// process that came before it under the same node id.
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

// bloemHeartbeatUpsertSQL is Silo's heartbeat upsert plus this process's
// instance id, which live TV routing matches so a restarted node is not taken
// for the process that owns a session.
const bloemHeartbeatUpsertSQL = `
		INSERT INTO node_heartbeats (node_id, node_type, node_url, updated_at, instance_id)
		VALUES ($1, $2, $3, NOW(), $4::uuid)
		ON CONFLICT (node_id) DO UPDATE SET
			node_type   = EXCLUDED.node_type,
			node_url    = EXCLUDED.node_url,
			updated_at  = NOW(),
			instance_id = EXCLUDED.instance_id
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
