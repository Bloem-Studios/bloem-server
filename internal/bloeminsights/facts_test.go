package bloeminsights

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestFactsFromATrustedEdge(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v2/stream/sess-1", nil)
	req.RemoteAddr = "127.0.0.2:51000"
	req.Header.Set(EdgeHeader, "name=ams-1; lat=52.37; lon=4.89; viewer_proto=h3")
	req.Header.Set("CF-Ray", "8c2b1a2f3e4d5a6b-AMS")
	now := time.Unix(1_800_000_000, 0)
	f := FactsFromRequest(req, true, false, now)
	if f.Edge == nil || f.Edge.Name != "ams-1" || f.Edge.ViewerProto != "h3" {
		t.Fatalf("edge = %+v, want ams-1 over h3", f.Edge)
	}
	if f.CFColo != "AMS" || f.Proto != "HTTP/1.1" || !f.At.Equal(now) {
		t.Fatalf("facts = %+v", f)
	}
}

func TestFactsIgnoreAnUntrustedPeer(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v2/stream/sess-1", nil)
	req.RemoteAddr = "203.0.113.9:51000"
	req.Header.Set(EdgeHeader, "name=ams-1; lat=52.37; lon=4.89; viewer_proto=h3")
	req.Header.Set("CF-Ray", "8c2b1a2f3e4d5a6b-AMS")
	f := FactsFromRequest(req, false, false, time.Now())
	if f.Edge != nil || f.CFColo != "" {
		t.Fatalf("an untrusted viewer painted a route: %+v", f)
	}
}

func TestFactsTakeCFRayFromACloudflarePeer(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v2/stream/sess-1", nil)
	req.RemoteAddr = "162.158.1.1:51000"
	req.Header.Set(EdgeHeader, "name=ams-1; lat=52.37; lon=4.89; viewer_proto=h3")
	req.Header.Set("CF-Ray", "8c2b1a2f3e4d5a6b-AMS")
	f := FactsFromRequest(req, false, true, time.Now())
	if f.CFColo != "AMS" {
		t.Fatalf("colo = %q, want AMS from a Cloudflare peer", f.CFColo)
	}
	if f.Edge != nil {
		t.Fatalf("a Cloudflare peer's edge header was trusted: %+v", f.Edge)
	}
}

func edgeFacts(proto string, at time.Time) RequestFacts {
	return RequestFacts{Proto: "HTTP/1.1", Edge: &EdgeHop{Name: "ams-1", Latitude: 52.37, Longitude: 4.89, ViewerProto: proto}, At: at}
}

func checkTallies(t *testing.T, store FactsStore, session string, now time.Time) {
	t.Helper()
	ctx := context.Background()
	for range 3 {
		if err := store.Record(ctx, session, edgeFacts("h2", now)); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 {
		if err := store.Record(ctx, session, edgeFacts("h3", now)); err != nil {
			t.Fatal(err)
		}
	}
	summary, ok, err := store.Summary(ctx, session)
	if err != nil || !ok {
		t.Fatalf("summary ok = %v err = %v", ok, err)
	}
	if summary.Counts[TransportTCP] != 3 || summary.Counts[TransportUDP] != 5 {
		t.Fatalf("counts = %v, want tcp 3 and udp 5", summary.Counts)
	}
	if !summary.Changed {
		t.Fatal("a stream that moved from tcp to udp is not marked changed")
	}
	if summary.Latest.Edge == nil || summary.Latest.Edge.ViewerProto != "h3" {
		t.Fatalf("latest = %+v, want the h3 request", summary.Latest)
	}
	if _, ok, err := store.Summary(ctx, session+"-other"); ok || err != nil {
		t.Fatalf("unknown session summary ok = %v err = %v, want none", ok, err)
	}
}

func TestMemoryFactsStoreTallies(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	checkTallies(t, NewMemoryFactsStore(6*time.Hour, func() time.Time { return now }), "sess-1", now)
}

func TestMemoryFactsStoreOneClassIsNotChanged(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryFactsStore(time.Hour, func() time.Time { return now })
	_ = store.Record(context.Background(), "sess-1", edgeFacts("h2", now))
	_ = store.Record(context.Background(), "sess-1", edgeFacts("h1", now))
	summary, _, _ := store.Summary(context.Background(), "sess-1")
	if summary.Changed || summary.Counts[TransportTCP] != 2 {
		t.Fatalf("summary = %+v, want two tcp requests and no change", summary)
	}
}

func TestMemoryFactsStoreExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	store := NewMemoryFactsStore(time.Hour, func() time.Time { return clock })
	if err := store.Record(context.Background(), "sess-1", edgeFacts("h2", now)); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(59 * time.Minute)
	if _, ok, _ := store.Summary(context.Background(), "sess-1"); !ok {
		t.Fatal("an entry inside its TTL is gone")
	}
	clock = now.Add(61 * time.Minute)
	if _, ok, _ := store.Summary(context.Background(), "sess-1"); ok {
		t.Fatal("an entry older than its TTL is still there")
	}
}

func TestRedisFactsStoreTallies(t *testing.T) {
	raw := os.Getenv("SILO_TEST_REDIS_URL")
	if raw == "" {
		t.Skip("SILO_TEST_REDIS_URL not set")
	}
	options, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatalf("parse SILO_TEST_REDIS_URL: %v", err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	session := "insights-" + uuid.NewString()
	t.Cleanup(func() { _ = client.Del(context.WithoutCancel(t.Context()), factsKey(session)).Err() })
	store := NewRedisFactsStore(client, time.Hour)
	checkTallies(t, store, session, time.Now().UTC().Truncate(time.Second))
	ttl, err := client.TTL(t.Context(), factsKey(session)).Result()
	if err != nil || ttl <= 0 || ttl > time.Hour {
		t.Fatalf("ttl = %v err = %v, want within an hour", ttl, err)
	}
}
