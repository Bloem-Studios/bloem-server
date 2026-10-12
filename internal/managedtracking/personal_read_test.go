package managedtracking

import (
	"context"
	"errors"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Server/silo-server/internal/watchsync"
)

type personalReadClient struct {
	watchsync.WatchSyncPluginClient
	onRead func()
	called bool
}

func (c *personalReadClient) ListRemoteState(context.Context, *pluginv1.WatchSyncListRemoteStateRequest) (*pluginv1.WatchSyncListRemoteStateResponse, error) {
	c.called = true
	if c.onRead != nil {
		c.onRead()
	}
	return &pluginv1.WatchSyncListRemoteStateResponse{CompleteSnapshot: true}, nil
}
func TestManagedPersonalReadRechecksAuthorityBeforePublication(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "during"}[during], func(t *testing.T) {
			f := enrolledFixture(t)
			ctx := t.Context()
			if _, err := f.s.Pool.Exec(ctx, `UPDATE watch_provider_connections SET import_favorites_enabled=true WHERE provider=$1`, f.key); err != nil {
				t.Fatal(err)
			}
			c := &personalReadClient{}
			revoke := func() {
				if err := f.s.Revoke(ctx, f.id); err != nil {
					t.Fatal(err)
				}
			}
			if during {
				c.onRead = revoke
			} else {
				revoke()
			}
			out, err := f.s.WrapClient(f.id, c).ListRemoteState(ctx, &pluginv1.WatchSyncListRemoteStateRequest{Context: f.authContext(), StateKinds: []pluginv1.WatchSyncRemoteStateKind{pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_FAVORITE}})
			if !errors.Is(err, ErrAuthority) || out != nil {
				t.Fatal("revoked authority published a remote snapshot", out, err)
			}
			if c.called != during {
				t.Fatal("preflight authority did not stop RPC")
			}
		})
	}
}
