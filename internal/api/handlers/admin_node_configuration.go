package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodepool"
)

type AdminNodeConfigurationStore interface {
	Snapshot(context.Context) ([]*nodepool.Node, int64, error)
	Create(context.Context, nodepool.CreateNodeInput) (*nodepool.Node, error)
	Update(context.Context, int, nodepool.UpdateNodeInput, func(int64) error) (*nodepool.Node, error)
	Delete(context.Context, int, func(int64) error) error
}

func (h *NodeHandler) SetConfigurationStore(store AdminNodeConfigurationStore) {
	h.configuration = store
	h.configurationWake = make(chan struct{}, 1)
}

func (h *NodeHandler) configurationChanged() {
	select {
	case h.configurationWake <- struct{}{}:
	default:
	}
}

func (h *NodeHandler) CreateAdminNode(ctx context.Context, input nodepool.CreateNodeInput) (*nodepool.Node, error) {
	if h.configuration == nil {
		return nil, ErrAdminNodesUnavailable
	}
	node, err := h.configuration.Create(ctx, input)
	if err == nil {
		h.configurationChanged()
	}
	return node, err
}
func (h *NodeHandler) UpdateAdminNode(ctx context.Context, id int, input nodepool.UpdateNodeInput, guard func(int64) error) (*nodepool.Node, error) {
	if h.configuration == nil {
		return nil, ErrAdminNodesUnavailable
	}
	node, err := h.configuration.Update(ctx, id, input, guard)
	if err == nil {
		h.configurationChanged()
	}
	return node, err
}
func (h *NodeHandler) DeleteAdminNode(ctx context.Context, id int, guard func(int64) error) error {
	if h.configuration == nil {
		return ErrAdminNodesUnavailable
	}
	err := h.configuration.Delete(ctx, id, guard)
	if err == nil {
		h.configurationChanged()
	}
	return err
}

// StartConfigurationReconciliation runs once per API replica, after dependency
// wiring. Startup and periodic reads recover missed wakeups, process death and
// failed reads. Every pass reapplies the durable snapshot: a late legacy event
// read can otherwise replace a newer pool even when the generation is unchanged.
// Worker policy watching remains the worker's responsibility; this loop makes no
// worker requests and does not claim a worker has reloaded or stopped sessions.
func (h *NodeHandler) StartConfigurationReconciliation(ctx context.Context) {
	if ctx == nil || h.configuration == nil {
		return
	}
	go func() {
		var previous []*nodepool.Node
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			attempt, cancel := context.WithTimeout(ctx, nodePostCommitTimeout)
			nodes, _, err := h.configuration.Snapshot(attempt)
			if err == nil {
				h.applyConfigurationSnapshot(previous, nodes)
				previous = nodes
			} else if ctx.Err() == nil {
				slog.WarnContext(ctx, "node configuration reconciliation will retry", "component", "api")
			}
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-h.configurationWake:
			}
		}
	}()
}

func (h *NodeHandler) applyConfigurationSnapshot(previous, nodes []*nodepool.Node) {
	if h.invalidateCapabilityCache != nil {
		current := make(map[int]*nodepool.Node, len(nodes))
		for _, node := range nodes {
			current[node.ID] = node
		}
		for _, old := range previous {
			next := current[old.ID]
			if next == nil || next.AdminRevision != old.AdminRevision {
				h.invalidateCapabilityCache(old.URL)
				if next != nil && next.URL != old.URL {
					h.invalidateCapabilityCache(next.URL)
				}
			}
		}
	}
	var proxy, transcode []*nodepool.Node
	for _, node := range nodes {
		if !node.Enabled {
			continue
		}
		copy := *node // SetNodes normalizes URLs; retain the stored configuration.
		switch node.Type {
		case nodepool.NodeTypeProxy:
			proxy = append(proxy, &copy)
		case nodepool.NodeTypeTranscode:
			transcode = append(transcode, &copy)
		}
	}
	if h.proxyPool != nil {
		h.proxyPool.SetNodes(proxy)
	}
	if h.transcodePool != nil {
		h.transcodePool.SetNodes(transcode)
	}
}
