package watchtogether

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestTwoReplicaRoomOwnershipIsFencedAndFailsOver(t *testing.T) {
	pool := openDistributedTestPool(t)
	store := NewPostgresRoomOwner(pool)
	ctx := context.Background()
	roomID := "distributed-owner-" + time.Now().UTC().Format("150405.000000000")
	now := time.Now().UTC()
	var userID int
	if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("test database has no user fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO watch_together_rooms (id, code, join_token, host_user_id, host_profile_id)
		VALUES ($1, $2, $3, $4, 'distributed-test-profile')`, roomID, roomID, roomID, userID); err != nil {
		t.Fatalf("insert room fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM watch_together_rooms WHERE id = $1`, roomID)
	})

	first, err := store.Acquire(ctx, roomID, "node-a", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Acquire(node-a): %v", err)
	}
	if _, err := store.Acquire(ctx, roomID, "node-b", now.Add(time.Minute)); !errors.Is(err, ErrRoomOwned) {
		t.Fatalf("Acquire(node-b) while leased error = %v, want ErrRoomOwned", err)
	}
	if err := store.Release(ctx, first); err != nil {
		t.Fatalf("Release(node-a): %v", err)
	}
	second, err := store.Acquire(ctx, roomID, "node-b", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Acquire(node-b) after release: %v", err)
	}
	if second.Generation <= first.Generation {
		t.Fatalf("failover generation = %d, want > %d", second.Generation, first.Generation)
	}
	if err := store.Release(ctx, first); !errors.Is(err, ErrRoomOwnerGenerationMismatch) {
		t.Fatalf("stale Release(node-a) error = %v, want generation mismatch", err)
	}
}

func TestTwoReplicaRelayDeliversGenerationTaggedCommandOnce(t *testing.T) {
	clientA := openDistributedTestRedis(t)
	clientB := openDistributedTestRedis(t)
	relayA := NewRedisRoomRelay(clientA)
	relayB := NewRedisRoomRelay(clientB)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan Command, 2)
	subscription, err := relayB.Subscribe(ctx, func(command Command) { received <- command })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer subscription.Close()

	command := Command{
		RoomID: "room-7", Generation: 42, CommandID: "command-1", Kind: "pause",
		Payload: json.RawMessage(`{"position_seconds":12}`),
	}
	if err := relayA.Publish(ctx, command); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-received:
		if got.RoomID != command.RoomID || got.Generation != command.Generation || got.CommandID != command.CommandID || got.Kind != command.Kind {
			t.Fatalf("relayed command = %#v, want %#v", got, command)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed command")
	}
	select {
	case duplicate := <-received:
		t.Fatalf("duplicate relay delivery: %#v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
}

func openDistributedTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func openDistributedTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	address := os.Getenv("SILO_TEST_REDIS_ADDR")
	if address == "" {
		address = "127.0.0.1:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		t.Skipf("Redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
