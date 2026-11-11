package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SearchProviderMeilisearchMirror is the provider of the search mirror's row in
// catalog_search_index_state. The row exists only once a mirror has been configured.
const SearchProviderMeilisearchMirror = "meilisearch_mirror"

// SearchMirrorStateName is the mirror's state as the admin page and the state metric show it.
type SearchMirrorStateName string

const (
	SearchMirrorFollowing     SearchMirrorStateName = "following"
	SearchMirrorCopying       SearchMirrorStateName = "copying"
	SearchMirrorWaiting       SearchMirrorStateName = "waiting"
	SearchMirrorUnreachable   SearchMirrorStateName = "unreachable"
	SearchMirrorNeedsFullCopy SearchMirrorStateName = "needs_full_copy"
	SearchMirrorMisconfigured SearchMirrorStateName = "misconfigured"
	SearchMirrorFailing       SearchMirrorStateName = "failing"
	SearchMirrorSettling      SearchMirrorStateName = "settling"
)

// SearchMirrorStateNames is every state, in the state metric's label order.
var SearchMirrorStateNames = []SearchMirrorStateName{
	SearchMirrorFollowing,
	SearchMirrorCopying,
	SearchMirrorWaiting,
	SearchMirrorUnreachable,
	SearchMirrorNeedsFullCopy,
	SearchMirrorMisconfigured,
	SearchMirrorFailing,
	SearchMirrorSettling,
}

// pgxQuerier, pgxExecer and pgxExecQuerier are what the mirror's SQL needs from a database
// handle. *pgxpool.Pool, pgx.Tx and *pgxpool.Conn satisfy all three. The first two are the
// package's existing interfaces of the same methods under the names the mirror's code reads
// by; they are aliases, not new types.
type (
	pgxQuerier = searchQuerier
	pgxExecer  = itemExecer
)

type pgxExecQuerier interface {
	pgxExecer
	pgxQuerier
}

// errSearchMirrorNotOwner stops a run that no longer owns the mirror: its row changed since the
// run loaded it (another run saved it), or its lock's session closed (a database failover drops
// advisory locks). The run stops quietly and records nothing; the run that took over carries on.
var errSearchMirrorNotOwner = errors.New("search mirror: another run took over the mirror")

// pgSnapshot is a Postgres pg_snapshot: transactions below Xmin had finished, transactions from
// Xmax on had not started, and those in Xip were still running.
type pgSnapshot struct {
	Xmin, Xmax uint64
	Xip        []uint64
}

// parsePgSnapshot reads pg_snapshot's text form, "xmin:xmax:xip,xip", with the checks Postgres
// makes on input: ids are non-zero, xmin <= xmax, and each running id is in [xmin, xmax), in
// ascending order. Postgres never writes a repeated id; one is refused here.
func parsePgSnapshot(text string) (pgSnapshot, error) {
	bad := func() (pgSnapshot, error) { return pgSnapshot{}, fmt.Errorf("invalid pg_snapshot %q", text) }
	parts := strings.Split(text, ":")
	if len(parts) != 3 {
		return bad()
	}
	xmin, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return bad()
	}
	xmax, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return bad()
	}
	if xmin == 0 || xmax == 0 || xmax < xmin {
		return bad()
	}
	snapshot := pgSnapshot{Xmin: xmin, Xmax: xmax}
	if parts[2] == "" {
		return snapshot, nil
	}
	for _, raw := range strings.Split(parts[2], ",") {
		xid, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || xid < xmin || xid >= xmax {
			return bad()
		}
		if n := len(snapshot.Xip); n > 0 && xid <= snapshot.Xip[n-1] {
			return bad()
		}
		snapshot.Xip = append(snapshot.Xip, xid)
	}
	return snapshot, nil
}

// String is the snapshot in pg_snapshot's text form.
func (s pgSnapshot) String() string {
	var b strings.Builder
	b.WriteString(strconv.FormatUint(s.Xmin, 10))
	b.WriteByte(':')
	b.WriteString(strconv.FormatUint(s.Xmax, 10))
	b.WriteByte(':')
	for i, xid := range s.Xip {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(xid, 10))
	}
	return b.String()
}

// searchMirrorWindow is a catch-up in progress: the events of transactions that finished
// between the bookmark and Upto, applied in (tx_id, id) order up to (AfterTx, AfterID).
type searchMirrorWindow struct {
	Upto    string    `json:"upto"`
	UptoAt  time.Time `json:"upto_at"`
	AfterTx uint64    `json:"after_tx"`
	AfterID int64     `json:"after_id"`
}

// searchMirrorCopy is a full copy in progress into the side index SideUID, to be published as
// TargetUID. Start is the snapshot taken when it began; it becomes the bookmark when it ends.
type searchMirrorCopy struct {
	SideUID        string    `json:"side_uid"`
	TargetUID      string    `json:"target_uid"`
	SchemaVersion  int       `json:"schema_version"`
	Start          string    `json:"start"`
	StartAt        time.Time `json:"start_at"`
	AfterContentID string    `json:"after_content_id"`
	Done           int       `json:"done"`
	Total          int       `json:"total"`
	StartedAt      time.Time `json:"started_at"`
	// ProgressAt is when the copy last made progress: its start, then each acknowledged batch.
	// The lag cap is measured from it while the copy runs, so a copy that keeps moving keeps
	// the rows it needs however long it takes.
	ProgressAt time.Time `json:"progress_at"`
}

// searchMirrorRetry is the page that failed: how often, and when it is tried next.
type searchMirrorRetry struct {
	Attempts  int       `json:"attempts"`
	NextAt    time.Time `json:"next_at"`
	LastError string    `json:"last_error"`
}

// searchMirrorSwap is the last role swap: when (database time), by which process, and whether
// it is still settling.
type searchMirrorSwap struct {
	At       time.Time `json:"at"`
	By       string    `json:"by"`
	Settling bool      `json:"settling"`
}

// searchMirrorState is the mirror's own state, kept as JSON in the bloem_mirror column of its
// catalog_search_index_state row.
type searchMirrorState struct {
	State  SearchMirrorStateName `json:"state,omitempty"`
	Reason string                `json:"reason,omitempty"`
	// Bookmark is the pg_snapshot whose transactions are all on the mirror; BookmarkAt is the
	// database time it was taken.
	Bookmark   string     `json:"bookmark,omitempty"`
	BookmarkAt *time.Time `json:"bookmark_at,omitempty"`

	Window *searchMirrorWindow `json:"window,omitempty"`
	Copy   *searchMirrorCopy   `json:"copy,omitempty"`
	Retry  *searchMirrorRetry  `json:"retry,omitempty"`
	Swap   *searchMirrorSwap   `json:"swap,omitempty"`

	// MainTarget and MirrorTarget are the identity markers remembered for each server.
	MainTarget   string `json:"main_target,omitempty"`
	MirrorTarget string `json:"mirror_target,omitempty"`

	LastError         string           `json:"last_error,omitempty"`
	ErrorSince        *time.Time       `json:"error_since,omitempty"`
	LastSuccessAt     *time.Time       `json:"last_success_at,omitempty"`
	DeadLetterRetryAt *time.Time       `json:"dead_letter_retry_at,omitempty"`
	MaxLagSeconds     int64            `json:"max_lag_seconds,omitempty"`
	Errors            map[string]int64 `json:"errors,omitempty"`
	RoleSwaps         int64            `json:"role_swaps,omitempty"`
	ForceCopy         bool             `json:"force_copy,omitempty"`

	// RebuildMain is the main index a failover found stale on the new main server: the main
	// rebuild is owed until the main state names another index. Each run starts it again.
	RebuildMain string `json:"rebuild_main,omitempty"`

	// Gen is the row's generation: every save requires the generation it loaded and stores the
	// next one, so a run that lost the mirror never overwrites a newer row.
	Gen int64 `json:"gen,omitempty"`
}

// hold is the snapshot the mirror still needs outbox rows after, and when it was taken: a
// copy's start while a copy is under way (the copy replaces whatever the bookmark covered),
// the bookmark otherwise. ok is false when the mirror holds nothing.
func (s searchMirrorState) hold() (snapshot string, at time.Time, ok bool) {
	if s.Copy != nil && s.Copy.Start != "" {
		return s.Copy.Start, s.Copy.StartAt, true
	}
	if s.Bookmark == "" {
		return "", time.Time{}, false
	}
	if s.BookmarkAt != nil {
		at = *s.BookmarkAt
	}
	return s.Bookmark, at, true
}

// holdSince is hold's snapshot with the time the lag cap is measured from, which the worker and
// the cleanup share:
//   - while a copy is under way, its last progress (a copy that keeps moving is never past
//     the cap, and keeps the rows since its start);
//   - otherwise the later of the bookmark's time and the last published copy: the cleanup kept
//     every row since the copy's start until the copy was published.
//
// since is zero when no time is recorded.
func (r searchMirrorRow) holdSince() (snapshot string, since time.Time, ok bool) {
	s := r.State
	if c := s.Copy; c != nil && c.Start != "" {
		return c.Start, latestTime(c.StartAt, c.ProgressAt), true
	}
	snapshot, since, ok = s.hold()
	if ok && r.LastRebuildAt != nil {
		since = latestTime(since, *r.LastRebuildAt)
	}
	return snapshot, since, ok
}

func latestTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// searchMirrorRow is the mirror's catalog_search_index_state row. The columns mean what they
// mean for the main index: the published index, its schema version and size, the last full
// copy and the last sync.
type searchMirrorRow struct {
	State          searchMirrorState
	ActiveIndexUID string
	SchemaVersion  int
	DocumentCount  int
	LastRebuildAt  *time.Time
	LastSyncAt     *time.Time
}

// loadSearchMirrorRow reads the mirror's row; ok is false when there is none. Column times
// come back in UTC.
func loadSearchMirrorRow(ctx context.Context, q pgxQuerier) (searchMirrorRow, bool, error) {
	var row searchMirrorRow
	var raw []byte
	err := q.QueryRow(ctx, `
		SELECT active_index_uid, schema_version, document_count, last_rebuild_at, last_sync_at, bloem_mirror
		FROM catalog_search_index_state
		WHERE provider = $1`, SearchProviderMeilisearchMirror).
		Scan(&row.ActiveIndexUID, &row.SchemaVersion, &row.DocumentCount, &row.LastRebuildAt, &row.LastSyncAt, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return searchMirrorRow{}, false, nil
	}
	if err != nil {
		return searchMirrorRow{}, false, fmt.Errorf("load search mirror state: %w", err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &row.State); err != nil {
			return searchMirrorRow{}, false, fmt.Errorf("decode search mirror state: %w", err)
		}
	}
	row.LastRebuildAt = utcTimePtr(row.LastRebuildAt)
	row.LastSyncAt = utcTimePtr(row.LastSyncAt)
	return row, true, nil
}

// saveSearchMirrorRow writes the mirror's row, replacing what was there, only if the stored
// generation is still the one row was loaded with (0 for no row). On success row.State.Gen is
// the stored generation. When another run saved the row in between, or it was deleted (a
// restore), nothing is written and the error is errSearchMirrorNotOwner.
func saveSearchMirrorRow(ctx context.Context, q pgxExecer, row *searchMirrorRow) error {
	loaded := row.State.Gen
	next := *row
	next.State.Gen = loaded + 1
	state, err := json.Marshal(next.State)
	if err != nil {
		return fmt.Errorf("encode search mirror state: %w", err)
	}
	args := []any{SearchProviderMeilisearchMirror, next.ActiveIndexUID, next.SchemaVersion, next.DocumentCount,
		next.LastRebuildAt, next.LastSyncAt, string(state), loaded}
	tag, err := q.Exec(ctx, `
		UPDATE catalog_search_index_state SET
			active_index_uid = $2,
			schema_version = $3,
			document_count = $4,
			last_rebuild_at = $5,
			last_sync_at = $6,
			bloem_mirror = $7::jsonb,
			updated_at = now()
		WHERE provider = $1
		  AND COALESCE((bloem_mirror->>'gen')::bigint, 0) = $8::bigint`, args...)
	if err == nil && tag.RowsAffected() == 0 && loaded == 0 {
		// No row yet: the first save inserts it. One that appeared meanwhile is another run's.
		tag, err = q.Exec(ctx, `
			INSERT INTO catalog_search_index_state (
				provider, active_index_uid, schema_version, document_count,
				last_rebuild_at, last_sync_at, bloem_mirror, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, now())
			ON CONFLICT (provider) DO NOTHING`, args[:7]...)
	}
	if err != nil {
		return fmt.Errorf("save search mirror state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errSearchMirrorNotOwner
	}
	row.State.Gen = next.State.Gen
	return nil
}

func utcTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// searchMirrorEffectiveMaxLag is the lag after which the mirror makes a full copy: configured,
// but at most SearchMirrorSwitchingMaxLag while bloem_silo_switching is enabled.
func searchMirrorEffectiveMaxLag(ctx context.Context, q pgxQuerier, configured time.Duration) (time.Duration, error) {
	var switching bool
	if err := q.QueryRow(ctx, `SELECT public.bloem_silo_switching_enabled()`).Scan(&switching); err != nil {
		return configured, fmt.Errorf("read silo switching: %w", err)
	}
	if switching && configured > SearchMirrorSwitchingMaxLag {
		return SearchMirrorSwitchingMaxLag, nil
	}
	return configured, nil
}

// touchSearchMirrorProcess writes this process's lease: inserted with the database clock on the
// first touch, its heartbeat moved on every later one. It returns the start time, which never
// changes after the first insert.
func touchSearchMirrorProcess(ctx context.Context, q pgxExecQuerier, instanceID, host string) (startedAt time.Time, err error) {
	err = q.QueryRow(ctx, `
		INSERT INTO bloem_search_mirror_processes (instance_id, host, started_at, heartbeat_at)
		VALUES ($1, $2, now(), now())
		ON CONFLICT (instance_id) DO UPDATE SET heartbeat_at = now()
		RETURNING started_at`, instanceID, host).Scan(&startedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("touch search mirror process lease: %w", err)
	}
	return startedAt, nil
}

// searchMirrorOlderProcessAlive reports whether a process that started before the swap at
// swapAt, other than swapBy which made it, has touched its lease within quiet.
func searchMirrorOlderProcessAlive(ctx context.Context, q pgxQuerier, swapAt time.Time, swapBy string, quiet time.Duration) (bool, error) {
	var alive bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM bloem_search_mirror_processes
			WHERE started_at < $1
			  AND instance_id::text <> $2
			  AND heartbeat_at > now() - $3::bigint * interval '1 microsecond'
		)`, swapAt, swapBy, quiet.Microseconds()).Scan(&alive)
	if err != nil {
		return false, fmt.Errorf("read search mirror process leases: %w", err)
	}
	return alive, nil
}
