package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodeidentity"
	"github.com/jackc/pgx/v5/pgconn"
)

type heartbeatStore interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
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
