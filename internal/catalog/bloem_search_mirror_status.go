package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The status route's fixed words. The state "off" means neither this server nor the database
// knows of a mirror; the warning codes are English data the admin page translates.
const (
	searchMirrorStatusOff         = "off"
	searchMirrorStatusEnvironment = "environment"

	searchMirrorWarnOneCopyOnly   = "configured_on_one_copy_only"
	searchMirrorWarnSettlingLate  = "settling_overdue"
	searchMirrorWarnStoodDown     = "stood_down"
	searchMirrorReasonNotStarted  = "not_started"
	searchMirrorStatusRedacted    = "[redacted]"
	searchMirrorStatusReadTimeout = 10 * time.Second
	searchMirrorStatusPendingWait = 5 * time.Second
)

// SearchMirrorCopyStatus is the progress of a full copy into a side index.
type SearchMirrorCopyStatus struct {
	Done      int        `json:"done" minimum:"0" doc:"Documents copied so far."`
	Total     int        `json:"total" minimum:"0" doc:"Documents the copy will hold; shown for progress only, so it may be 0 or approximate."`
	Ratio     float64    `json:"ratio" minimum:"0" maximum:"1" doc:"done / total, 0 to 1."`
	StartedAt *time.Time `json:"started_at" nullable:"true" doc:"When the copy began (UTC); null when unknown."`
}

// SearchMirrorStatus is what GET /api/bloem/v2/admin/search/mirror returns: the mirror's
// configuration (never its key), its state and how far behind it is. The web type is
// web/src/api/bloemSearchMirror.ts; change both together.
type SearchMirrorStatus struct {
	Configured bool   `json:"configured" doc:"True when this server has the mirror address set."`
	Source     string `json:"source" doc:"Where the settings came from: environment, or an empty string when not configured."`
	URL        string `json:"url" doc:"The mirror's address with any user name and password removed; empty when not configured."`
	KeySource  string `json:"key_source" doc:"Where the mirror's API key comes from: file, environment or none; an empty string when not configured. The key itself is never returned."`

	State           string     `json:"state" doc:"One of off, following, copying, waiting, unreachable, needs_full_copy, misconfigured, failing or settling. A newer server may send more."`
	Reason          string     `json:"reason,omitempty" doc:"An English code saying why, such as past_cap or key_file_unreadable; omitted when there is none."`
	Settling        bool       `json:"settling" doc:"True while a role swap is settling: the new main index is being brought up to date."`
	SettlingOverdue bool       `json:"settling_overdue" doc:"True when a swap has been settling for more than 30 minutes."`
	LagSeconds      *float64   `json:"lag_seconds" nullable:"true" minimum:"0" doc:"Seconds between now and the moment the mirror is up to date as of; null when the mirror holds no bookmark yet."`
	UpToDateAsOf    *time.Time `json:"up_to_date_as_of" nullable:"true" doc:"Every change committed before this time (UTC) is on the mirror; null when the mirror holds no bookmark yet."`

	PendingEvents       *int64 `json:"pending_events" nullable:"true" minimum:"0" doc:"Outbox rows the mirror has not applied; null when the mirror holds nothing yet or the count was unavailable."`
	PendingEventsCapped bool   `json:"pending_events_capped" doc:"True when pending_events stopped counting at its limit of 1,000,000."`

	MirrorIndex string                  `json:"mirror_index" doc:"The index the mirror has published; empty before the first copy."`
	MainIndex   string                  `json:"main_index" doc:"The index the main Meilisearch has published; empty when none."`
	Copy        *SearchMirrorCopyStatus `json:"copy" doc:"The full copy in progress; null when none."`

	LastFullCopyAt *time.Time `json:"last_full_copy_at" nullable:"true" doc:"When the mirror last finished a full copy (UTC); null when never."`
	LastError      string     `json:"last_error,omitempty" doc:"The last failure, in English; omitted when there is none."`
	ErrorSince     *time.Time `json:"error_since" nullable:"true" doc:"When the current run of failures began (UTC); null when there is none."`

	DeadLetters   int64 `json:"dead_letters" minimum:"0" doc:"Items the mirror could not index and set aside."`
	MaxLagSeconds int64 `json:"max_lag_seconds" minimum:"0" doc:"The longest lag, in seconds, before the mirror stops catching up and copies everything again; 0 until the mirror has run."`
	RoleSwaps     int64 `json:"role_swaps" minimum:"0" doc:"Failovers the mirror has handled."`

	StateExists bool     `json:"state_exists" doc:"True when this database holds mirror state, whether or not this server is configured."`
	Warnings    []string `json:"warnings" nullable:"false" doc:"Warning codes: configured_on_one_copy_only, settling_overdue and stood_down today; a newer server may send more."`
}

// SearchMirrorStatusFor reads the mirror's status. It is read-only and takes no lock: it
// combines what this process was started with (the runtime registry) with the mirror's row
// in the database, so any Silo process answers for the shared state. The only error is a
// database one.
func SearchMirrorStatusFor(ctx context.Context, pool *pgxpool.Pool) (SearchMirrorStatus, error) {
	if pool == nil {
		return SearchMirrorStatus{}, errors.New("search mirror status: no database")
	}
	ctx, cancel := context.WithTimeout(ctx, searchMirrorStatusReadTimeout)
	defer cancel()

	cfg, mirror := searchMirrorRuntime()
	status := SearchMirrorStatus{
		Configured: cfg.Configured,
		State:      searchMirrorStatusOff,
		Warnings:   []string{},
	}
	if cfg.Configured {
		status.Source = searchMirrorStatusEnvironment
		status.URL = cfg.RedactedURL()
		status.KeySource = cfg.KeySource
	}

	read, err := readSearchMirrorStatusRows(ctx, pool)
	if err != nil {
		return SearchMirrorStatus{}, err
	}
	row, exists, now := read.row, read.exists, read.now
	status.StateExists = exists
	if !cfg.Configured && !exists {
		return status, nil
	}

	s := row.State
	// A configured mirror whose worker has not yet written its row has not started.
	switch {
	case !exists && cfg.Problem == "":
		status.State = string(SearchMirrorWaiting)
		status.Reason = searchMirrorReasonNotStarted
	default:
		status.State = string(searchMirrorDisplayState(s, cfg.Problem))
		status.Reason = s.Reason
		if cfg.Problem != "" {
			status.Reason = string(cfg.Problem)
		}
	}

	status.Settling = s.Swap != nil && s.Swap.Settling
	status.SettlingOverdue = searchMirrorSettlingOverdue(s, now, SearchMirrorOptions{}.withDefaults().SettleWarnAfter)

	// Lag is measured from the snapshot the mirror holds, as the lag metric measures it and as
	// pending_events counts from: the copy's start while a copy runs, the bookmark otherwise.
	if _, holdAt, holding := s.hold(); holding && !holdAt.IsZero() {
		asOf := holdAt.UTC()
		lag := max(now.Sub(asOf).Truncate(time.Millisecond).Seconds(), 0)
		status.UpToDateAsOf = &asOf
		status.LagSeconds = &lag
	}

	status.MirrorIndex = row.ActiveIndexUID
	status.MainIndex = read.mainIndex
	if c := s.Copy; c != nil {
		copied := &SearchMirrorCopyStatus{Done: c.Done, Total: c.Total}
		if c.Total > 0 {
			copied.Ratio = min(max(float64(c.Done)/float64(c.Total), 0), 1)
		}
		if !c.StartedAt.IsZero() {
			startedAt := c.StartedAt.UTC()
			copied.StartedAt = &startedAt
		}
		status.Copy = copied
	}

	status.LastFullCopyAt = row.LastRebuildAt
	status.LastError = redactSearchMirrorKey(s.LastError, cfg.APIKey)
	status.ErrorSince = utcTimePtr(s.ErrorSince)
	status.DeadLetters = read.deadLetters
	status.MaxLagSeconds = s.MaxLagSeconds
	status.RoleSwaps = s.RoleSwaps

	if !cfg.Configured && exists {
		status.Warnings = append(status.Warnings, searchMirrorWarnOneCopyOnly)
	}
	if status.SettlingOverdue {
		status.Warnings = append(status.Warnings, searchMirrorWarnSettlingLate)
	}
	if mirror != nil && mirror.StoodDown() {
		status.Warnings = append(status.Warnings, searchMirrorWarnStoodDown)
	}

	// The count scans the outbox and is the one figure that can time out on a huge backlog;
	// the page is more useful without it than not at all.
	if hold, _, ok := s.hold(); ok {
		countCtx, cancelCount := context.WithTimeout(ctx, searchMirrorStatusPendingWait)
		pending, capped, err := searchMirrorPendingCount(countCtx, pool, hold)
		cancelCount()
		switch {
		case err == nil:
			status.PendingEvents = &pending
			status.PendingEventsCapped = capped
		case ctx.Err() != nil:
			return SearchMirrorStatus{}, fmt.Errorf("count search mirror pending events: %w", err)
		}
	}
	return status, nil
}

// searchMirrorStatusRows is what the status needs from the database, read in one snapshot.
type searchMirrorStatusRows struct {
	row         searchMirrorRow
	exists      bool
	now         time.Time // the database clock
	deadLetters int64
	mainIndex   string // the main Meilisearch's published index, empty when none
}

func readSearchMirrorStatusRows(ctx context.Context, pool *pgxpool.Pool) (searchMirrorStatusRows, error) {
	var read searchMirrorStatusRows
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return read, fmt.Errorf("read search mirror status: %w", err)
	}
	// The transaction only reads, so ending it is a rollback.
	defer func() { _ = tx.Rollback(ctx) }()

	read.row, read.exists, err = loadSearchMirrorRow(ctx, tx)
	if err != nil {
		return read, err
	}
	err = tx.QueryRow(ctx, `
		SELECT now(),
		       (SELECT count(*) FROM bloem_search_mirror_dead_letters),
		       COALESCE((SELECT active_index_uid FROM catalog_search_index_state WHERE provider = $1), '')`,
		SearchProviderMeilisearch).Scan(&read.now, &read.deadLetters, &read.mainIndex)
	if err != nil {
		return read, fmt.Errorf("read search mirror figures: %w", err)
	}
	read.now = read.now.UTC()
	return read, nil
}

// redactSearchMirrorKey hides this process's mirror key from a stored message, should an
// error text have carried it. A short or empty key is left alone: replacing it would mangle
// the text without protecting anything.
func redactSearchMirrorKey(message, key string) string {
	if message == "" || len(key) < 8 {
		return message
	}
	return strings.ReplaceAll(message, key, searchMirrorStatusRedacted)
}
