package nativestorage

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRedisCoverCacheRoundTrip(t *testing.T) {
	url := os.Getenv("SILO_TEST_REDIS_URL")
	if url == "" {
		t.Skip("SILO_TEST_REDIS_URL is not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	cache := NewRedisCoverCache(client, time.Hour)
	key := "bloem:storage-cover:test:" + uuid.NewString()
	t.Cleanup(func() { client.Del(t.Context(), key) })
	if data, err := cache.Get(t.Context(), key); data != nil || err != nil {
		t.Fatalf("miss = %q %v", data, err)
	}
	cover := bytes.Repeat([]byte{0xFF, 0xD8}, 50_000)
	if err := cache.Set(t.Context(), key, cover); err != nil {
		t.Fatal(err)
	}
	if data, err := cache.Get(t.Context(), key); err != nil || !bytes.Equal(data, cover) {
		t.Fatalf("hit = %d bytes %v", len(data), err)
	}
	if ttl := client.TTL(t.Context(), key).Val(); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("ttl = %s", ttl)
	}
}
