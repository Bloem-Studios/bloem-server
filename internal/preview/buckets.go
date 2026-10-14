package preview

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"sync"
	"time"
)

// ClientBuckets turns a client address into an opaque bucket: the IPv4
// address or the IPv6 /64, keyed with a secret that never leaves this
// process. A restart makes old buckets unlinkable.
type ClientBuckets struct{ key []byte }

func NewClientBuckets() *ClientBuckets {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("preview: no randomness for client buckets: " + err.Error())
	}
	return &ClientBuckets{key: key}
}

// NewClientBucketsWithSecret keeps the client cap consistent across server
// processes. Only the opaque bucket is persisted; the derived key stays in memory.
func NewClientBucketsWithSecret(secret string) *ClientBuckets {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("bloem.preview.client-buckets.v1"))
	return &ClientBuckets{key: mac.Sum(nil)}
}

// Key returns the bucket for an address; an unparseable one shares a bucket.
func (b *ClientBuckets) Key(ip string) string {
	prefix := "unknown"
	if addr, err := netip.ParseAddr(ip); err == nil {
		addr = addr.Unmap()
		bits := 32
		if addr.Is6() {
			bits = 64
		}
		if p, err := addr.Prefix(bits); err == nil {
			prefix = p.String()
		}
	}
	mac := hmac.New(sha256.New, b.key)
	mac.Write([]byte(prefix))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// HourlyLimiter allows limit events per key in any rolling hour, in memory.
type HourlyLimiter struct {
	limit   int
	mu      sync.Mutex
	hits    map[string][]time.Time
	pruneAt time.Time
	now     func() time.Time
}

func NewHourlyLimiter(limit int) *HourlyLimiter {
	return &HourlyLimiter{limit: limit, hits: make(map[string][]time.Time), now: time.Now}
}

// Allow records an event for key, or reports how long until one is allowed.
func (l *HourlyLimiter) Allow(key string) (bool, time.Duration) {
	now := l.now()
	cutoff := now.Add(-time.Hour)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit <= 0 {
		return false, time.Hour
	}
	if !now.Before(l.pruneAt) {
		for k, v := range l.hits {
			if len(v) == 0 || !v[len(v)-1].After(cutoff) {
				delete(l.hits, k)
			}
		}
		l.pruneAt = now.Add(time.Minute)
	}
	if _, known := l.hits[key]; !known && len(l.hits) >= 100_000 {
		return false, time.Minute
	}
	kept := l.hits[key][:0]
	for _, at := range l.hits[key] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false, kept[0].Sub(cutoff)
	}
	l.hits[key] = append(kept, now)

	return true, 0
}
