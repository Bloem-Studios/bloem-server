package livetv

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/nodeidentity"
	"github.com/Silo-Server/silo-server/internal/worker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLiveTVUnlimitedEncodesCountWhenLimitChanges(t *testing.T) {
	bridge := &HLSBridge{}
	if !bridge.acquireTranscodeSlot(-1) || bridge.acquireTranscodeSlot(1) {
		t.Fatal("unlimited encode disappeared from the finite limit")
	}
	bridge.releaseTranscodeSlot(true)
	if !bridge.acquireTranscodeSlot(1) {
		t.Fatal("released encode still occupies capacity")
	}
}

func TestLiveTVClusterOwnerAndCompatLedger(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgStore(pool)
	tuner, err := store.CreateTuner(ctx, &Tuner{Type: TunerTypeHDHomeRun, DeviceID: uuid.NewString(), TunerCount: 2, Status: "ready", BaseURL: "http://192.168.1.2"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DeleteTuner(ctx, tuner.ID); err != nil {
			t.Error(err)
		}
	}()
	ch := Channel{ID: uuid.NewString(), TunerID: tuner.ID, Number: "1", Name: "test", Enabled: true, StreamURL: "http://192.168.1.2/auto/v1"}
	if err := store.ReplaceChannelsForTuner(ctx, tuner.ID, []Channel{ch}); err != nil {
		t.Fatal(err)
	}
	channels, err := store.ListChannels(ctx, tuner.ID)
	if err != nil || len(channels) != 1 {
		t.Fatalf("channels %v %v", channels, err)
	}
	ch = channels[0]
	a := NewService(pool)
	b := NewService(pool)
	nodeA := "cluster-test-" + uuid.NewString()
	a.SetClusterOwner(nodeA, nodeidentity.InstanceID())
	b.SetClusterOwner("other-api", uuid.NewString())
	heartbeat := worker.NewHeartbeatWriter(pool, nodeA, "api", "http://api-a.internal:8080")
	if err := heartbeat.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := heartbeat.CleanupSelf(ctx); err != nil {
			t.Error(err)
		}
	}()
	live, err := store.CreateSession(ctx, SessionCreate{ChannelID: ch.ID, TunerID: tuner.ID, UserID: 7, ProfileID: "p", PlaybackSessionID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if target, err := a.PeerHLSURL(ctx, live.PlaybackSessionID, 7, "p", true); err != nil || target != "" {
		t.Fatalf("legacy local route %q %v", target, err)
	}
	if err := a.bindSessionOwner(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	if target, err := a.PeerHLSURL(ctx, live.PlaybackSessionID, 7, "p", true); err != nil || target != "" {
		t.Fatalf("local route %q %v", target, err)
	}
	if target, err := b.PeerHLSURL(ctx, live.PlaybackSessionID, 7, "p", true); err != nil || target != "http://api-a.internal:8080" {
		t.Fatalf("peer route %q %v", target, err)
	}
	for _, identity := range []struct {
		user    int
		profile string
	}{{8, "p"}, {7, "other"}, {7, ""}} {
		if _, err := b.PeerHLSURL(ctx, live.PlaybackSessionID, identity.user, identity.profile, true); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign route granted: %v", err)
		}
	}
	// A heartbeat under the same node name must not authorize another process.
	if _, err := pool.Exec(ctx, `UPDATE livetv_sessions SET owner_instance_id=$2 WHERE id=$1`, live.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.PeerHLSURL(ctx, live.PlaybackSessionID, 7, "p", true); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatalf("reused node name stole route: %v", err)
	}
	if err := a.bindSessionOwner(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	stream := CompatStream{ID: uuid.NewString(), NativeSession: live.ID}
	if err := a.PutCompatStream(ctx, stream, "opener-token"); err != nil {
		t.Fatal(err)
	}
	if got, err := b.GetCompatStream(ctx, stream.ID, "opener-token"); err != nil || got.NativeSession != live.ID {
		t.Fatalf("remote compat lookup %+v %v", got, err)
	}
	if _, err := b.GetCompatStream(ctx, stream.ID, "foreign-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign opener allowed %v", err)
	}
	var hash string
	if err := pool.QueryRow(ctx, `SELECT opener_hash FROM bloem_livetv_compat_streams WHERE id=$1`, stream.ID).Scan(&hash); err != nil || hash == "opener-token" || len(hash) != 64 {
		t.Fatalf("credential not hashed: %v", err)
	}
	if err := heartbeat.CleanupSelf(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.PeerHLSURL(ctx, live.PlaybackSessionID, 7, "p", true); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatalf("dead owner: %v", err)
	}
	if _, err := b.ReleaseSession(ctx, live.ID, 7, "p", true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetCompatStream(ctx, stream.ID, "opener-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("released compat stream: %v", err)
	}
	if _, err := a.PeerHLSURL(ctx, live.PlaybackSessionID, 7, "p", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("released HLS route: %v", err)
	}
}
