package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodepool"
)

type configurationSnapshot struct {
	nodes []*nodepool.Node
	err   error
}
type scriptedNodeConfiguration struct {
	snapshots chan configurationSnapshot
	reads     chan struct{}
}

func (s *scriptedNodeConfiguration) Snapshot(ctx context.Context) ([]*nodepool.Node, int64, error) {
	select {
	case s.reads <- struct{}{}:
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	select {
	case out := <-s.snapshots:
		return out.nodes, 1, out.err
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
}
func (s *scriptedNodeConfiguration) Create(context.Context, nodepool.CreateNodeInput) (*nodepool.Node, error) {
	return nil, errors.New("unused")
}
func (s *scriptedNodeConfiguration) Update(context.Context, int, nodepool.UpdateNodeInput, func(int64) error) (*nodepool.Node, error) {
	return nil, errors.New("unused")
}
func (s *scriptedNodeConfiguration) Delete(context.Context, int, func(int64) error) error {
	return errors.New("unused")
}
func TestNodeConfigurationReconciliation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := &scriptedNodeConfiguration{make(chan configurationSnapshot, 3), make(chan struct{}, 3)}
	pool := nodepool.NewProxyPool()
	h := &NodeHandler{proxyPool: pool}
	h.SetConfigurationStore(s)
	node := &nodepool.Node{ID: 1, AdminRevision: 1, Type: nodepool.NodeTypeProxy, URL: "http://node.invalid/", Enabled: true}
	pool.SetNodes([]*nodepool.Node{{ID: 9, URL: "http://stale.invalid", Enabled: true}})
	s.snapshots <- configurationSnapshot{err: errors.New("synthetic read failure")}
	h.StartConfigurationReconciliation(ctx)
	select {
	case <-s.reads:
	case <-time.After(time.Second):
		t.Fatal("no startup reconciliation")
	}
	s.snapshots <- configurationSnapshot{nodes: []*nodepool.Node{node}}
	h.configurationChanged()
	waitNodeConfiguration(t, func() bool { return pool.FindByURL("http://node.invalid") != nil })
	if pool.FindByURL("http://stale.invalid") != nil {
		t.Fatal("stale pool retained")
	}
	if node.URL != "http://node.invalid/" {
		t.Fatal("pool mutated stored snapshot")
	}
	// A late bridge/event read can repopulate old nodes. Another reconciliation
	// reapplies the authoritative empty list even with the same generation.
	pool.SetNodes([]*nodepool.Node{{ID: 9, URL: "http://stale.invalid", Enabled: true}})
	s.snapshots <- configurationSnapshot{}
	h.configurationChanged()
	waitNodeConfiguration(t, func() bool { return pool.FindByURL("http://stale.invalid") == nil })
}

func waitNodeConfiguration(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !ready() {
		select {
		case <-deadline.C:
			t.Fatal("configuration did not converge")
		case <-tick.C:
		}
	}
}
