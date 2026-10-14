package preview

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"
)

var ErrInvalidEvent = errors.New("preview: invalid event")

func ValidEvent(event string) bool {
	return event == "cta_shown" || event == "cta_clicked" || event == "session_ended"
}

type Events struct{ pool *pgxpool.Pool }

func NewEvents(pool *pgxpool.Pool) *Events { return &Events{pool: pool} }
func (e *Events) Increment(ctx context.Context, event string) error {
	if !ValidEvent(event) {
		return ErrInvalidEvent
	}
	_, err := e.pool.Exec(ctx, `INSERT INTO bloem_preview_event_counts(day,event,count) VALUES((now() AT TIME ZONE 'UTC')::date,$1,1) ON CONFLICT(day,event) DO UPDATE SET count=bloem_preview_event_counts.count+1`, event)
	return err
}

type eventSession struct {
	limiter *rate.Limiter
	seen    time.Time
}

// Session keys stay in bounded process memory; persistent counters contain no identities.
type EventLimiter struct {
	mu       sync.Mutex
	all      *rate.Limiter
	sessions map[string]*eventSession
	now      func() time.Time
	pruneAt  time.Time
}

func NewEventLimiter() *EventLimiter {
	return &EventLimiter{all: rate.NewLimiter(5, 20), sessions: map[string]*eventSession{}, now: time.Now}
}
func (l *EventLimiter) Allow(session string) bool {
	if session == "" {
		return false
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.After(l.pruneAt) {
		for key, value := range l.sessions {
			if now.Sub(value.seen) > time.Hour {
				delete(l.sessions, key)
			}
		}
		l.pruneAt = now.Add(time.Minute)
	}
	bucket := l.sessions[session]
	if bucket == nil {
		if len(l.sessions) >= 10000 {
			return false
		}
		bucket = &eventSession{limiter: rate.NewLimiter(rate.Every(10*time.Second), 3)}
		l.sessions[session] = bucket
	}
	bucket.seen = now
	return bucket.limiter.AllowN(now, 1) && l.all.AllowN(now, 1)
}
