package bloeminsights

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RequestFacts are what one media request tells about its stream's
// connection: the protocol it reached this server with, the edge that
// forwarded it and the Cloudflare colo it passed, both only under trust.
type RequestFacts struct {
	Proto  string    `json:"proto,omitempty"`
	Edge   *EdgeHop  `json:"edge,omitempty"`
	CFColo string    `json:"cf_colo,omitempty"`
	At     time.Time `json:"at"`
}

// FactsFromRequest reads r's facts. The edge header counts only when the
// transport peer is a trusted proxy, and CF-Ray only then or when the peer
// is a Cloudflare address: a viewer can send either header itself.
func FactsFromRequest(r *http.Request, peerTrusted, cloudflarePeer bool, now time.Time) RequestFacts {
	facts := RequestFacts{Proto: r.Proto, At: now}
	if peerTrusted {
		if hop, ok := ParseEdgeHeader(r.Header.Get(EdgeHeader)); ok {
			facts.Edge = &hop
		}
	}
	if peerTrusted || cloudflarePeer {
		if colo, ok := CloudflareColo(r.Header.Get("CF-Ray")); ok {
			facts.CFColo = colo
		}
	}
	return facts
}

// FactsSummary is what a stream's media requests said so far: the latest
// request's facts, how many requests each transport class carried, and
// whether more than one class carried them.
type FactsSummary struct {
	Latest  RequestFacts
	Counts  map[Transport]int64
	Changed bool
}

// Majority is the class that carried most requests, preferring the latest
// request's class on a tie, or TransportUnknown with no requests.
func (s FactsSummary) Majority() Transport {
	best, bestCount := TransportUnknown, int64(0)
	latest := ClassifyViewerLeg(s.Latest)
	for _, class := range Transports {
		count := s.Counts[class]
		if count > bestCount || (count == bestCount && count > 0 && class == latest) {
			best, bestCount = class, count
		}
	}
	return best
}

func changed(counts map[Transport]int64) bool {
	seen := 0
	for _, count := range counts {
		if count > 0 {
			seen++
		}
	}
	return seen > 1
}

// FactsStore keeps request facts per playback session for a while.
type FactsStore interface {
	Record(ctx context.Context, sessionID string, f RequestFacts) error
	Summary(ctx context.Context, sessionID string) (FactsSummary, bool, error)
}

// NewMemoryFactsStore keeps facts in this process, for a server without
// Redis. now is the clock; nil means time.Now.
func NewMemoryFactsStore(ttl time.Duration, now func() time.Time) FactsStore {
	if now == nil {
		now = time.Now
	}
	return &memoryFactsStore{ttl: ttl, now: now, entries: map[string]*memoryFacts{}}
}

type memoryFacts struct {
	latest  RequestFacts
	counts  map[Transport]int64
	expires time.Time
}

type memoryFactsStore struct {
	mu        sync.Mutex
	ttl       time.Duration
	now       func() time.Time
	entries   map[string]*memoryFacts
	lastSweep time.Time
}

func (s *memoryFactsStore) Record(_ context.Context, sessionID string, f RequestFacts) error {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.lastSweep) >= time.Minute {
		for id, entry := range s.entries {
			if !now.Before(entry.expires) {
				delete(s.entries, id)
			}
		}
		s.lastSweep = now
	}
	entry := s.entries[sessionID]
	if entry == nil || !now.Before(entry.expires) {
		entry = &memoryFacts{counts: map[Transport]int64{}}
		s.entries[sessionID] = entry
	}
	entry.latest = f
	entry.counts[ClassifyViewerLeg(f)]++
	entry.expires = now.Add(s.ttl)
	return nil
}

func (s *memoryFactsStore) Summary(_ context.Context, sessionID string) (FactsSummary, bool, error) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[sessionID]
	if entry == nil || !now.Before(entry.expires) {
		return FactsSummary{}, false, nil
	}
	counts := make(map[Transport]int64, len(entry.counts))
	for class, count := range entry.counts {
		counts[class] = count
	}
	return FactsSummary{Latest: entry.latest, Counts: counts, Changed: changed(counts)}, true, nil
}

// NewRedisFactsStore keeps facts in Redis, so every API replica sees the
// requests the others served. Each session is one hash,
// bloem:insights:facts:<session_id>, with a counter per class and the
// latest facts as JSON; every write refreshes its TTL.
func NewRedisFactsStore(c *redis.Client, ttl time.Duration) FactsStore {
	return &redisFactsStore{client: c, ttl: ttl}
}

type redisFactsStore struct {
	client *redis.Client
	ttl    time.Duration
}

const (
	factsCountPrefix = "c:"
	factsLatestField = "latest"
)

func factsKey(sessionID string) string { return "bloem:insights:facts:" + sessionID }

func (s *redisFactsStore) Record(ctx context.Context, sessionID string, f RequestFacts) error {
	latest, err := json.Marshal(f)
	if err != nil {
		return err
	}
	key := factsKey(sessionID)
	_, err = s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HIncrBy(ctx, key, factsCountPrefix+string(ClassifyViewerLeg(f)), 1)
		pipe.HSet(ctx, key, factsLatestField, latest)
		pipe.Expire(ctx, key, s.ttl)
		return nil
	})
	return err
}

func (s *redisFactsStore) Summary(ctx context.Context, sessionID string) (FactsSummary, bool, error) {
	fields, err := s.client.HGetAll(ctx, factsKey(sessionID)).Result()
	if err != nil {
		return FactsSummary{}, false, err
	}
	if len(fields) == 0 {
		return FactsSummary{}, false, nil
	}
	summary := FactsSummary{Counts: map[Transport]int64{}}
	for field, value := range fields {
		if class, ok := strings.CutPrefix(field, factsCountPrefix); ok {
			count, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return FactsSummary{}, false, fmt.Errorf("facts counter %s: %w", field, err)
			}
			summary.Counts[Transport(class)] = count
		}
	}
	if latest := fields[factsLatestField]; latest != "" {
		if err := json.Unmarshal([]byte(latest), &summary.Latest); err != nil {
			return FactsSummary{}, false, fmt.Errorf("latest facts: %w", err)
		}
	}
	summary.Changed = changed(summary.Counts)
	return summary, true, nil
}
