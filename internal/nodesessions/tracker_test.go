package nodesessions

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestExecutorGenerationRedis(t *testing.T) {
	raw := os.Getenv("SILO_TEST_REDIS_URL")
	if raw == "" {
		t.Skip("SILO_TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	node := uuid.NewString()
	old := NewTracker(client, node, "test", "transcode")
	next := NewTracker(client, node, "test", "transcode")
	a := playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
	b := a
	b.Epoch++
	b.ExecutorID = uuid.NewString()
	info := SessionInfo{SessionID: uuid.NewString(), Executor: &a}
	successor := info
	successor.Executor = &b
	oldKey, _ := old.infoKey(info)
	nextKey, _ := next.infoKey(successor)
	legacyKey := old.redisKey(info.SessionID)
	t.Cleanup(func() { client.Del(context.Background(), oldKey, nextKey, legacyKey) })
	ctx := t.Context()
	old.Track(ctx, info)
	next.Track(ctx, successor)
	assertSuccessor := func() {
		t.Helper()
		if exists, err := client.Exists(ctx, nextKey).Result(); err != nil || exists != 1 {
			t.Fatalf("successor lost: %d %v", exists, err)
		}
	}
	old.RemoveExecutor(ctx, info.SessionID, a)
	assertSuccessor()
	old.Track(ctx, info)
	if err := client.PExpire(ctx, nextKey, 5*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	old.refreshAll(ctx)
	ttl, err := client.PTTL(ctx, nextKey).Result()
	if err != nil || ttl > 5*time.Second {
		t.Fatalf("stale refresh extended successor: %v %v", ttl, err)
	}
	old.Touch(ctx, info)
	old.Cleanup(ctx)
	assertSuccessor()
	old.Remove(ctx, info.SessionID)
	assertSuccessor()
	if count := next.ActiveCount(); count != 1 {
		t.Fatalf("successor local tracking lost: %d", count)
	}
	next.RemoveExecutor(ctx, info.SessionID, b)
	if n := client.Exists(ctx, nextKey).Val(); n != 0 {
		t.Fatal("own generation remove failed")
	}
	legacy := SessionInfo{SessionID: info.SessionID}
	old.Track(ctx, legacy)
	if client.Exists(ctx, legacyKey).Val() != 1 {
		t.Fatal("legacy key changed")
	}
	old.Remove(ctx, info.SessionID)
	if client.Exists(ctx, legacyKey).Val() != 0 {
		t.Fatal("legacy remove changed")
	}
	malformed := info
	malformed.Executor = new(playback.ExecutorNamespaceV3)
	old.Track(ctx, malformed)
	if old.ActiveCount() != 0 {
		t.Fatal("invalid executor tracked")
	}
}
