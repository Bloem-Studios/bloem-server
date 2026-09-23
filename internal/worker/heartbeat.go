package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// HeartbeatWriter periodically upserts a row in node_heartbeats to signal
// this node is alive. All node types (integrated, api, proxy, transcode)
// should run a HeartbeatWriter.
type HeartbeatWriter struct {
	pool     heartbeatStore
	nodeID   string
	nodeType string
	nodeURL  string
	interval time.Duration
	stop     chan struct{}
	bloemHB
}

// NewHeartbeatWriter creates a HeartbeatWriter for the given node identity.
func NewHeartbeatWriter(pool *pgxpool.Pool, nodeID, nodeType, nodeURL string) *HeartbeatWriter {
	return &HeartbeatWriter{
		pool:     pool,
		nodeID:   nodeID,
		nodeType: nodeType,
		nodeURL:  nodeURL,
		interval: 15 * time.Second,
		stop:     make(chan struct{}),
		bloemHB:  newBloemHB(),
	}
}

// Beat performs a single heartbeat upsert.
func (hw *HeartbeatWriter) Beat(ctx context.Context) error {
	_, err := hw.pool.Exec(ctx, bloemHeartbeatUpsertSQL, hw.nodeID, hw.nodeType, hw.nodeURL, hw.instanceID)
	if err != nil {
		return fmt.Errorf("heartbeat upsert: %w", err)
	}
	return nil
}

// Start begins the background heartbeat loop. Runs until Stop is called.
func (hw *HeartbeatWriter) Start() {
	if hw.bloemStart() {
		return
	}
	go func() {
		// Beat immediately on start so the node is visible right away.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := hw.Beat(ctx); err != nil {
			slog.Error("initial heartbeat failed", "error", err, "node", hw.nodeID)
		}
		cancel()

		ticker := time.NewTicker(hw.interval)
		defer ticker.Stop()

		for {
			select {
			case <-hw.stop:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := hw.Beat(ctx); err != nil {
					slog.Error("heartbeat failed", "error", err, "node", hw.nodeID)
				}
				cancel()
			}
		}
	}()
}

// Stop signals the heartbeat loop to stop.
func (hw *HeartbeatWriter) Stop() {
	if hw.bloemStop() {
		return
	}
	close(hw.stop)
}

// CleanupSelf removes this node's heartbeat row and all its sessions from
// playback_sessions_sync. Call during graceful shutdown.
func (hw *HeartbeatWriter) CleanupSelf(ctx context.Context) error {
	_, err := hw.pool.Exec(ctx, `
		DELETE FROM playback_sessions_sync WHERE reporting_node = $1
	`, hw.nodeID)
	if err != nil {
		return fmt.Errorf("deleting sessions for node %s: %w", hw.nodeID, err)
	}

	_, err = hw.pool.Exec(ctx, bloemHeartbeatCleanupSQL, hw.nodeID, hw.instanceID)
	if err != nil {
		return fmt.Errorf("deleting heartbeat for node %s: %w", hw.nodeID, err)
	}
	return nil
}
