package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// The search mirror's metrics. Like workmetrics.QueueSampler they are sampled from the shared
// database in the background, never while Prometheus scrapes, so every Silo process reports the
// same values. A process exports them only while the mirror is configured.
const (
	searchMirrorSampleEvery   = 30 * time.Second
	searchMirrorSampleTimeout = 2 * time.Second
	// searchMirrorSampleMaxAge is how old a sample may be and still be exported.
	searchMirrorSampleMaxAge = 90 * time.Second
	// searchMirrorPendingCap bounds the pending-events count so a long outage stays cheap to read.
	searchMirrorPendingCap = 1_000_000
)

// searchMirrorErrorKinds are the kinds errors_total exports, in the order it exports them. The
// state row's Errors map is never exported as it is: its keys are not all labels, and raw
// error text stays out of the metrics.
var searchMirrorErrorKinds = []string{"unreachable", "rejected", "database"}

const searchMirrorMetricNote = "Sampled from the shared database; aggregate replicas with max, not sum."

var (
	searchMirrorStateDesc = prometheus.NewDesc("silo_search_mirror_state",
		"Search mirror state: 1 for the current state, 0 for the others. "+searchMirrorMetricNote, []string{"state"}, nil)
	searchMirrorLagDesc = prometheus.NewDesc("silo_search_mirror_lag_seconds",
		"Seconds since the snapshot the mirror holds was taken; absent while the mirror holds none. "+searchMirrorMetricNote, nil, nil)
	searchMirrorPendingDesc = prometheus.NewDesc("silo_search_mirror_pending_events",
		"Outbox rows the mirror has not applied, counted up to 1000000; absent while the mirror holds no snapshot. "+searchMirrorMetricNote, nil, nil)
	searchMirrorDeadLettersDesc = prometheus.NewDesc("silo_search_mirror_dead_letters",
		"Items the mirror could not index. "+searchMirrorMetricNote, nil, nil)
	searchMirrorLastSuccessDesc = prometheus.NewDesc("silo_search_mirror_last_success_timestamp_seconds",
		"Unix time of the last page or copy batch the mirror applied; absent until there is one. "+searchMirrorMetricNote, nil, nil)
	searchMirrorCopyProgressDesc = prometheus.NewDesc("silo_search_mirror_copy_progress_ratio",
		"Progress of a running full copy, 0 to 1; 0 when none is running. "+searchMirrorMetricNote, nil, nil)
	searchMirrorErrorsDesc = prometheus.NewDesc("silo_search_mirror_errors_total",
		"Mirror failures by kind (unreachable, rejected, database); no raw error text is exported. "+searchMirrorMetricNote, []string{"kind"}, nil)
	searchMirrorRoleSwapsDesc = prometheus.NewDesc("silo_search_mirror_role_swaps_total",
		"Failovers the mirror handled. "+searchMirrorMetricNote, nil, nil)
)

// searchMirrorSample is one reading of the mirror's state in the database.
type searchMirrorSample struct {
	at           time.Time
	state        SearchMirrorStateName
	lagSeconds   *float64
	pending      *int64
	deadLetters  int64
	lastSuccess  *time.Time
	copyProgress float64
	errors       map[string]int64
	roleSwaps    int64
}

// searchMirrorCollector samples the mirror's row every `every` and serves the last sample.
type searchMirrorCollector struct {
	pool  *pgxpool.Pool
	cfg   SearchMirrorConfig
	every time.Duration
	now   func() time.Time

	mu     sync.RWMutex
	sample *searchMirrorSample // nil until the first sample succeeds; never changed once stored
}

// newSearchMirrorCollector builds a collector. It does not query until sampleOnce runs. cfg
// supplies only Problem; the collector never holds or shows the key.
func newSearchMirrorCollector(pool *pgxpool.Pool, cfg SearchMirrorConfig, every time.Duration) *searchMirrorCollector {
	if every <= 0 {
		every = searchMirrorSampleEvery
	}
	return &searchMirrorCollector{pool: pool, cfg: cfg, every: every, now: time.Now}
}

// StartSearchMirrorMetrics registers the mirror's collector on the default registry and samples
// it in the background until ctx ends, when it unregisters. A mirror that is not configured
// exports nothing. A mirror that is configured but misconfigured does export, and shows as
// misconfigured.
func StartSearchMirrorMetrics(ctx context.Context, pool *pgxpool.Pool, cfg SearchMirrorConfig) error {
	if !cfg.Configured {
		return nil
	}
	c := newSearchMirrorCollector(pool, cfg, searchMirrorSampleEvery)
	if err := prometheus.Register(c); err != nil {
		return err
	}
	go c.run(ctx)
	return nil
}

// run samples now and then every c.every until ctx ends. A sampling failure is logged once per
// run of failures; the last good sample keeps being served until it is too old.
func (c *searchMirrorCollector) run(ctx context.Context) {
	defer prometheus.Unregister(c)
	ticker := time.NewTicker(c.every)
	defer ticker.Stop()
	failing := false
	for {
		err := c.sampleOnce(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil && !failing:
			failing = true
			slog.WarnContext(ctx, "search mirror: metrics sampling failed", "component", "catalog", "error", err)
		case err == nil && failing:
			failing = false
			slog.InfoContext(ctx, "search mirror: metrics sampling recovered", "component", "catalog")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sampleOnce reads the mirror once, within one 2 s budget, and replaces the stored sample. On an
// error the stored sample stays as it was.
func (c *searchMirrorCollector) sampleOnce(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, searchMirrorSampleTimeout)
	defer cancel()

	row, found, err := loadSearchMirrorRow(ctx, c.pool)
	if err != nil {
		return err
	}
	var state searchMirrorState
	if found {
		state = row.State
	}
	sample := searchMirrorSample{
		state:       searchMirrorDisplayState(state, c.cfg.Problem),
		lastSuccess: state.LastSuccessAt,
		errors:      make(map[string]int64, len(searchMirrorErrorKinds)),
		roleSwaps:   state.RoleSwaps,
	}
	for _, kind := range searchMirrorErrorKinds {
		sample.errors[kind] = state.Errors[kind]
	}
	// A copy that has begun counts as running, so its progress stays visible while it waits out
	// an outage.
	if state.Copy != nil && state.Copy.Total > 0 {
		sample.copyProgress = min(1, max(0, float64(state.Copy.Done)/float64(state.Copy.Total)))
	}

	hold, holdAt, holding := state.hold()
	var holdTime *time.Time
	if holding && !holdAt.IsZero() {
		holdTime = &holdAt
	}
	var lag *float64
	if err := c.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM bloem_search_mirror_dead_letters),
		       EXTRACT(EPOCH FROM now() - $1::timestamptz)::float8`, holdTime).
		Scan(&sample.deadLetters, &lag); err != nil {
		return fmt.Errorf("read search mirror counts: %w", err)
	}
	if lag != nil {
		seconds := max(0, *lag)
		sample.lagSeconds = &seconds
	}
	// A stored snapshot that does not parse would fail the count every time and blind every
	// other series, so it is left without a pending count; the worker deals with the bookmark.
	if holding {
		if _, parseErr := parsePgSnapshot(hold); parseErr == nil {
			pending, _, err := searchMirrorPendingCount(ctx, c.pool, hold)
			if err != nil {
				return err
			}
			sample.pending = &pending
		}
	}

	sample.at = c.now()
	c.mu.Lock()
	c.sample = &sample
	c.mu.Unlock()
	return nil
}

// searchMirrorPendingCount counts the outbox rows the snapshot hold does not cover, which are the
// ones the mirror still has to apply, up to searchMirrorPendingCap; capped says the cap was hit.
func searchMirrorPendingCount(ctx context.Context, q pgxQuerier, hold string) (n int64, capped bool, err error) {
	err = q.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT 1 FROM catalog_search_index_events
			WHERE provider = 'meilisearch'
			  AND tx_id >= pg_snapshot_xmin($1::text::pg_snapshot)
			  AND NOT pg_visible_in_snapshot(tx_id, $1::text::pg_snapshot)
			LIMIT $2::bigint
		) t`, hold, int64(searchMirrorPendingCap)).Scan(&n)
	if err != nil {
		return 0, false, fmt.Errorf("count search mirror pending events: %w", err)
	}
	return n, n >= searchMirrorPendingCap, nil
}

// searchMirrorDisplayState is the state the admin page and the state metric show. Ruling 4's
// precedence is misconfigured > unreachable > failing > needs_full_copy > copying > waiting >
// settling > following. The row holds one state at a time, so it comes down to this: a problem
// with the configuration is misconfigured whatever the row says; the row's own state shows as
// it is; and settling shows only in place of following. A row with no state yet, or one this
// build does not know, is waiting.
func searchMirrorDisplayState(s searchMirrorState, problem SearchMirrorProblem) SearchMirrorStateName {
	if problem != "" {
		return SearchMirrorMisconfigured
	}
	switch s.State {
	case SearchMirrorMisconfigured, SearchMirrorUnreachable, SearchMirrorFailing,
		SearchMirrorNeedsFullCopy, SearchMirrorCopying, SearchMirrorWaiting:
		return s.State
	case SearchMirrorFollowing, SearchMirrorSettling:
		if s.Swap != nil && s.Swap.Settling {
			return SearchMirrorSettling
		}
		return SearchMirrorFollowing
	default:
		return SearchMirrorWaiting
	}
}

// Describe sends every series this collector can export.
func (c *searchMirrorCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		searchMirrorStateDesc, searchMirrorLagDesc, searchMirrorPendingDesc, searchMirrorDeadLettersDesc,
		searchMirrorLastSuccessDesc, searchMirrorCopyProgressDesc, searchMirrorErrorsDesc, searchMirrorRoleSwapsDesc,
	} {
		ch <- d
	}
}

// Collect serves the last sample, or nothing when there is none or it is older than 90 s.
func (c *searchMirrorCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	s := c.sample
	c.mu.RUnlock()
	if s == nil || c.now().Sub(s.at) > searchMirrorSampleMaxAge {
		return
	}
	for _, name := range SearchMirrorStateNames {
		value := 0.0
		if name == s.state {
			value = 1
		}
		ch <- prometheus.MustNewConstMetric(searchMirrorStateDesc, prometheus.GaugeValue, value, string(name))
	}
	if s.lagSeconds != nil {
		ch <- prometheus.MustNewConstMetric(searchMirrorLagDesc, prometheus.GaugeValue, *s.lagSeconds)
	}
	if s.pending != nil {
		ch <- prometheus.MustNewConstMetric(searchMirrorPendingDesc, prometheus.GaugeValue, float64(*s.pending))
	}
	ch <- prometheus.MustNewConstMetric(searchMirrorDeadLettersDesc, prometheus.GaugeValue, float64(s.deadLetters))
	if s.lastSuccess != nil {
		ch <- prometheus.MustNewConstMetric(searchMirrorLastSuccessDesc, prometheus.GaugeValue, float64(s.lastSuccess.Unix()))
	}
	ch <- prometheus.MustNewConstMetric(searchMirrorCopyProgressDesc, prometheus.GaugeValue, s.copyProgress)
	for _, kind := range searchMirrorErrorKinds {
		ch <- prometheus.MustNewConstMetric(searchMirrorErrorsDesc, prometheus.CounterValue, float64(max(0, s.errors[kind])), kind)
	}
	ch <- prometheus.MustNewConstMetric(searchMirrorRoleSwapsDesc, prometheus.CounterValue, float64(max(0, s.roleSwaps)))
}
