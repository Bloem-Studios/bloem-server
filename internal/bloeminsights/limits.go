package bloeminsights

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// Count is how much of a limit is in use. Max 0 means no limit.
type Count struct {
	Active int `json:"active"`
	Max    int `json:"max"`
}

// PoolUsage is a shared pool: a household's streams and transcodes, or an
// organization's transcodes. A nil count is a limit the pool does not have.
type PoolUsage struct {
	ID         string `json:"id"`
	Streams    *Count `json:"streams,omitempty"`
	Transcodes *Count `json:"transcodes,omitempty"`
}

// PersonUsage is one person's live use against their limits.
type PersonUsage struct {
	UserID       int        `json:"user_id"`
	Username     string     `json:"username"`
	Streams      Count      `json:"streams"`
	Transcodes   Count      `json:"transcodes"`
	Household    *PoolUsage `json:"household,omitempty"`
	Organization *PoolUsage `json:"organization,omitempty"`
}

// LiveSession is the part of a live admin session row usage counts.
type LiveSession struct {
	UserID              int
	Username            string
	EffectivePlayMethod string
}

// LimitsFunc returns an account's current limits.
type LimitsFunc func(ctx context.Context, userID int) (playback.SessionLimits, error)

// ComputeUsage counts each person's live streams and transcodes against
// their limits. A session counts as a transcode when its whole-session
// method is "transcode". Household and organization pools are summed over
// every live session whose account shares the pool.
func ComputeUsage(ctx context.Context, sessions []LiveSession, limits LimitsFunc) ([]PersonUsage, error) {
	people := map[int]*PersonUsage{}
	var order []int
	accountLimits := map[int]playback.SessionLimits{}
	households := map[int]*PoolUsage{}
	organizations := map[string]*PoolUsage{}
	for _, s := range sessions {
		person, ok := people[s.UserID]
		if !ok {
			l, err := limits(ctx, s.UserID)
			if err != nil {
				return nil, err
			}
			accountLimits[s.UserID] = l
			person = &PersonUsage{UserID: s.UserID, Username: s.Username,
				Streams: Count{Max: l.MaxStreams}, Transcodes: Count{Max: l.MaxTranscodes}}
			if l.Household.ID > 0 {
				pool := households[l.Household.ID]
				if pool == nil {
					pool = &PoolUsage{ID: strconv.Itoa(l.Household.ID),
						Streams: &Count{Max: l.Household.MaxStreams}, Transcodes: &Count{Max: l.Household.MaxTranscodes}}
					households[l.Household.ID] = pool
				}
				person.Household = pool
			}
			if l.TenantID != "" && l.TenantMaxTranscodes > 0 {
				pool := organizations[l.TenantID]
				if pool == nil {
					pool = &PoolUsage{ID: l.TenantID, Transcodes: &Count{Max: l.TenantMaxTranscodes}}
					organizations[l.TenantID] = pool
				}
				person.Organization = pool
			}
			people[s.UserID] = person
			order = append(order, s.UserID)
		}
		transcode := s.EffectivePlayMethod == "transcode"
		person.Streams.Active++
		if transcode {
			person.Transcodes.Active++
		}
		if person.Household != nil {
			person.Household.Streams.Active++
			if transcode {
				person.Household.Transcodes.Active++
			}
		}
		if person.Organization != nil && transcode {
			person.Organization.Transcodes.Active++
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := people[order[i]], people[order[j]]
		if a.Username != b.Username {
			return a.Username < b.Username
		}
		return a.UserID < b.UserID
	})
	out := make([]PersonUsage, 0, len(order))
	for _, id := range order {
		out = append(out, *people[id])
	}
	return out, nil
}

// refusalQueueSize bounds the refusals waiting to be recorded.
const refusalQueueSize = 1024

// refusalCoalesce is the window in which repeats of one person's refusal
// at one limit make no new row.
const refusalCoalesce = 60 * time.Second

// LimitNotifier sends an admin push; *notifications.AdminPushService.
type LimitNotifier interface {
	Notify(ctx context.Context, n notifications.AdminPushNotice) error
}

// RefusalLimitsFunc returns the limits a refusal was made under.
type RefusalLimitsFunc func(ctx context.Context, userID int, profileID string) (playback.SessionLimits, error)

type refusalItem struct {
	userID    int
	profileID string
	kind      playback.LimitKind
	scope     playback.LimitScope
}

type refusalKey struct {
	userID int
	kind   playback.LimitKind
	scope  playback.LimitScope
}

// RefusalRecorder records every refusal at a stream or transcode limit and
// sends the matching limit push. It implements
// playback.LimitRefusalObserver: LimitRefused only queues, so the refused
// request never waits on the database or a push.
type RefusalRecorder struct {
	pool    *pgxpool.Pool
	notify  LimitNotifier
	limits  RefusalLimitsFunc
	logger  *slog.Logger
	now     func() time.Time
	queue   chan refusalItem
	dropped atomic.Int64

	mu     sync.Mutex
	recent map[refusalKey]time.Time
}

// NewRefusalRecorder builds a recorder; Run must be started for it to
// record. notify and limits may be nil (no pushes).
func NewRefusalRecorder(pool *pgxpool.Pool, notify LimitNotifier, limits RefusalLimitsFunc, logger *slog.Logger) *RefusalRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &RefusalRecorder{pool: pool, notify: notify, limits: limits, logger: logger, now: time.Now,
		queue: make(chan refusalItem, refusalQueueSize), recent: map[refusalKey]time.Time{}}
}

// Dropped counts refusals a full queue dropped.
func (r *RefusalRecorder) Dropped() int64 { return r.dropped.Load() }

// LimitRefused queues a refusal.
func (r *RefusalRecorder) LimitRefused(_ context.Context, userID int, profileID string, kind playback.LimitKind, scope playback.LimitScope) {
	select {
	case r.queue <- refusalItem{userID: userID, profileID: profileID, kind: kind, scope: scope}:
	default:
		r.dropped.Add(1)
	}
}

// Run records queued refusals until ctx ends.
func (r *RefusalRecorder) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-r.queue:
			r.record(ctx, item)
		}
	}
}

// record writes one refusal row unless the same person was refused at the
// same limit within refusalCoalesce (on this server, or on another replica
// by the row it wrote), then sends the push for it.
func (r *RefusalRecorder) record(ctx context.Context, item refusalItem) {
	if r.pool == nil || item.userID <= 0 {
		return
	}
	now := r.now()
	key := refusalKey{userID: item.userID, kind: item.kind, scope: item.scope}
	r.mu.Lock()
	if last, ok := r.recent[key]; ok && now.Sub(last) < refusalCoalesce {
		r.mu.Unlock()
		return
	}
	r.recent[key] = now
	for k, at := range r.recent {
		if now.Sub(at) >= refusalCoalesce {
			delete(r.recent, k)
		}
	}
	r.mu.Unlock()

	tag, err := r.pool.Exec(ctx, `
		INSERT INTO bloem_limit_refusals (user_id, profile_id, kind, scope, refused_at)
		SELECT $1, $2, $3, $4, $5
		WHERE NOT EXISTS (
			SELECT 1 FROM bloem_limit_refusals
			WHERE user_id = $1 AND kind = $3 AND scope = $4 AND refused_at > $5::timestamptz - $6::interval)`,
		item.userID, item.profileID, string(item.kind), string(item.scope), now, refusalCoalesce.String())
	if err != nil {
		if ctx.Err() == nil {
			r.logger.WarnContext(ctx, "stream insights could not record a limit refusal", "user_id", item.userID, "error", err)
		}
		return
	}
	if tag.RowsAffected() == 0 || r.notify == nil {
		return
	}
	notice := r.notice(ctx, item, now)
	if err := r.notify.Notify(ctx, notice); err != nil && ctx.Err() == nil {
		r.logger.WarnContext(ctx, "limit push failed", "user_id", item.userID, "kind", item.kind, "error", err)
	}
}

func (r *RefusalRecorder) notice(ctx context.Context, item refusalItem, at time.Time) notifications.AdminPushNotice {
	var person string
	_ = r.pool.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, item.userID).Scan(&person)
	max := 0
	if r.limits != nil {
		if l, err := r.limits(ctx, item.userID, item.profileID); err == nil {
			max = limitMax(l, item.kind, item.scope)
		}
	}
	if item.kind == playback.LimitStreams {
		return notifications.StreamLimitNotice(item.userID, person, max, string(item.scope), at)
	}
	return notifications.TranscodeLimitNotice(item.userID, person, max, string(item.scope), at)
}

func limitMax(l playback.SessionLimits, kind playback.LimitKind, scope playback.LimitScope) int {
	switch {
	case scope == playback.ScopeHousehold && kind == playback.LimitStreams:
		return l.Household.MaxStreams
	case scope == playback.ScopeHousehold:
		return l.Household.MaxTranscodes
	case scope == playback.ScopeOrganization:
		return l.TenantMaxTranscodes
	case kind == playback.LimitStreams:
		return l.MaxStreams
	}
	return l.MaxTranscodes
}
