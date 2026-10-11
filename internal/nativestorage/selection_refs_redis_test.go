package nativestorage

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func browseRedisFixture(t *testing.T) (*RedisBrowseCursorStore, *redis.Client) {
	t.Helper()
	raw := os.Getenv("SILO_TEST_REDIS_URL")
	if raw == "" {
		t.Skip("SILO_TEST_REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil || opts.Addr != "127.0.0.1:65421" || opts.Username != "" || opts.Password != "" {
		t.Fatal("refusing non-disposable browse Redis")
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { client.Close() })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	return NewRedisBrowseCursorStore(client), client
}
func TestRedisBrowseCursorStoreRetentionBounds(t *testing.T) {
	store, client := browseRedisFixture(t)
	type retained struct{ key, actor string }
	keys := make([]retained, 0)
	t.Cleanup(func() {
		for _, r := range keys {
			if err := store.Delete(context.Background(), r.key, r.actor); err != nil {
				t.Error("cursor cleanup", err)
			}
		}
	})
	actor := uuid.NewString()
	for i := 0; i < 16; i++ {
		key := browseStateKey(uuid.New())
		if err := store.Put(t.Context(), key, actor, "", []byte("immutable"), time.Minute); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, retained{key, actor})
	}
	blocked := browseStateKey(uuid.New())
	if err := store.Put(t.Context(), blocked, actor, "", []byte("extra"), time.Minute); err == nil {
		keys = append(keys, retained{blocked, actor})
		t.Fatal("actor retention quota exceeded")
	}
	// Replacement at quota must retire old evidence only after saving new evidence.
	next := browseStateKey(uuid.New())
	if err := store.Put(t.Context(), next, actor, keys[0].key, []byte("next"), time.Minute); err != nil {
		t.Fatal("replacement counted as an extra resident cursor", err)
	}
	keys = append(keys, retained{next, actor})
	if data, err := store.Get(t.Context(), keys[0].key); err != nil || len(data) != 0 {
		t.Fatal("replaced cursor remains resident")
	}
	if count := client.ZCard(t.Context(), browseStatePrefix+"actor:"+actor).Val(); count != 16 {
		t.Fatalf("actor count after replacement: %d", count)
	}
	// A key collision must not evict its replacement target or overwrite bytes.
	if err := store.Put(t.Context(), next, actor, keys[1].key, []byte("overwrite"), time.Minute); err == nil {
		t.Fatal("immutable value overwritten")
	}
	if data, err := store.Get(t.Context(), keys[1].key); err != nil || string(data) != "immutable" {
		t.Fatal("failed replacement destroyed previous state")
	}
	for i := 0; i < 7; i++ {
		other := uuid.NewString()
		for j := 0; j < 16; j++ {
			key := browseStateKey(uuid.New())
			if err := store.Put(t.Context(), key, other, "", []byte("global"), time.Minute); err != nil {
				t.Fatal(err)
			}
			keys = append(keys, retained{key, other})
		}
	}
	key := browseStateKey(uuid.New())
	other := uuid.NewString()
	if err := store.Put(t.Context(), key, other, "", []byte("overflow"), time.Minute); err == nil {
		keys = append(keys, retained{key, other})
		t.Fatal("global retention quota exceeded")
	}
}
func TestRedisBrowseCursorStoreBytesAndExpiry(t *testing.T) {
	store, client := browseRedisFixture(t)
	actor := uuid.NewString()
	key := browseStateKey(uuid.New())
	short := browseStateKey(uuid.New())
	large := browseStateKey(uuid.New())
	t.Cleanup(func() {
		for _, k := range []string{key, short, large} {
			if err := store.Delete(context.Background(), k, actor); err != nil {
				t.Error("cursor cleanup", err)
			}
		}
	})
	if err := store.Put(t.Context(), key, actor, "", []byte("longer lived"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), short, actor, "", []byte("short lived"), 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if ttl := client.PTTL(t.Context(), browseStatePrefix+"actor:"+actor).Val(); ttl < time.Minute {
		t.Fatal("short state shortened retention index TTL", ttl)
	}
	if err := store.Put(t.Context(), large, actor, "", []byte(strings.Repeat("x", (512<<10)+1)), time.Minute); err == nil {
		t.Fatal("oversized store write accepted")
	}
	if err := store.Put(t.Context(), large, actor, "", []byte("too long"), 16*time.Minute); err == nil {
		t.Fatal("unbounded TTL accepted")
	}
	if err := client.Set(t.Context(), large, strings.Repeat("x", (512<<10)+1), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), large); err == nil {
		t.Fatal("oversized Redis read accepted")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		data, err := store.Get(ctx, short)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == 0 {
			break
		}
		select {
		case <-poll.C:
		case <-ctx.Done():
			t.Fatal("state did not expire")
		}
	}
	if data, err := store.Get(t.Context(), key); err != nil || string(data) != "longer lived" {
		t.Fatal("short expiry removed unrelated cursor")
	}
}

func TestRedisBrowseCursorStoreReplacementRequiresLiveActorState(t *testing.T) {
	store, _ := browseRedisFixture(t)
	actor := uuid.NewString()
	previous := browseStateKey(uuid.New())
	next := browseStateKey(uuid.New())
	blocked := browseStateKey(uuid.New())
	otherActor := uuid.NewString()
	t.Cleanup(func() {
		for _, key := range []string{previous, next, blocked} {
			if err := store.Delete(context.Background(), key, actor); err != nil {
				t.Error("cursor cleanup", err)
			}
			if err := store.Delete(context.Background(), key, otherActor); err != nil {
				t.Error("cursor cleanup", err)
			}
		}
	})
	if err := store.Put(t.Context(), previous, actor, "", []byte("previous"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), blocked, otherActor, previous, []byte("wrong actor"), time.Minute); err == nil {
		t.Fatal("another actor consumed previous cursor state")
	}
	if err := store.Put(t.Context(), next, actor, previous, []byte("next"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), blocked, actor, previous, []byte("replay"), time.Minute); err == nil {
		t.Fatal("already consumed cursor state replaced again")
	}
	if data, err := store.Get(t.Context(), next); err != nil || string(data) != "next" {
		t.Fatal("failed replay disturbed retained continuation", err)
	}
	if data, err := store.Get(t.Context(), blocked); err != nil || len(data) != 0 {
		t.Fatal("failed replacement published state", err)
	}
}

func TestRedisBrowseCursorStoreConsumeRequiresLiveActorState(t *testing.T) {
	store, client := browseRedisFixture(t)
	actor, otherActor := uuid.NewString(), uuid.NewString()
	key := browseStateKey(uuid.New())
	t.Cleanup(func() {
		if err := store.Delete(context.Background(), key, actor); err != nil {
			t.Error("cursor cleanup", err)
		}
	})
	if err := store.Put(t.Context(), key, actor, "", []byte("terminal evidence"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Consume(t.Context(), key, otherActor); !errors.Is(err, errBrowseCursorRestart) {
		t.Fatalf("wrong actor consume: %v", err)
	}
	if data, err := store.Get(t.Context(), key); err != nil || string(data) != "terminal evidence" {
		t.Fatalf("wrong actor destroyed retained state: %q, %v", data, err)
	}
	for _, index := range []string{browseStatePrefix + "all", browseStatePrefix + "actor:" + actor} {
		if err := client.ZScore(t.Context(), index, key).Err(); err != nil {
			t.Fatal("wrong actor destroyed index membership", err)
		}
	}
	if err := store.Consume(t.Context(), key, actor); err != nil {
		t.Fatal("rightful consume", err)
	}
	if err := store.Consume(t.Context(), key, actor); !errors.Is(err, errBrowseCursorRestart) {
		t.Fatalf("replayed consume: %v", err)
	}
	if data, err := store.Get(t.Context(), key); err != nil || len(data) != 0 {
		t.Fatal("consumed state remains", err)
	}
	for _, index := range []string{browseStatePrefix + "all", browseStatePrefix + "actor:" + actor} {
		if err := client.ZScore(t.Context(), index, key).Err(); err != redis.Nil {
			t.Fatal("consumed index membership remains", err)
		}
	}
	if err := store.Delete(t.Context(), key, actor); err != nil {
		t.Fatal("cleanup must remain idempotent", err)
	}
}
