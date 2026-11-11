package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
	"github.com/Silo-Server/silo-server/internal/nodeidentity"
)

// The search mirror's production timings and identities. SearchMirrorOptions' zero values
// stand for these.
const (
	searchMirrorRunBudget            = 5 * time.Minute
	searchMirrorSettleQuiet          = 2 * time.Minute
	searchMirrorSettleWarnAfter      = 30 * time.Minute
	searchMirrorRetryBase            = 30 * time.Second
	searchMirrorRetryMax             = 15 * time.Minute
	searchMirrorMaxAttempts          = 10
	searchMirrorDeadLetterRetryEvery = time.Hour
	searchMirrorCopyQueueDepth       = 2
	searchMirrorLeaseEvery           = 15 * time.Second
	// searchMirrorLockName names the mirror's own advisory lock.
	searchMirrorLockName = "catalog.search_mirror"
)

// SearchMirrorOptions tunes a SearchMirror. The zero value is production: withDefaults fills
// every zero field. Tests shorten the timings and give each test its own lock keys.
type SearchMirrorOptions struct {
	RunBudget            time.Duration // how long one run applies pages back to back; 5m
	SettleQuiet          time.Duration // how long older processes must be silent before settling; 2m
	SettleWarnAfter      time.Duration // when settling becomes a warning; 30m
	RetryBase            time.Duration // a failed page waits RetryBase × tries; 30s
	RetryMax             time.Duration // up to RetryMax; 15m
	MaxAttempts          int           // tries before a page is split; 10
	DeadLetterRetryEvery time.Duration // 1h
	CopyQueueDepth       int           // Meilisearch tasks in flight during a copy; 2
	MaxPayloadBytes      int           // meilisearchMaxDocumentPayloadBytes
	LeaseEvery           time.Duration // the process lease's touch interval; 15s
	MirrorLockKey        int64         // pglock.Key("catalog.search_mirror")
	MainLockKey          int64         // searchIndexMaintenanceLockID
	InstanceID           string        // nodeidentity.InstanceID()
	Host                 string        // os.Hostname()
	// StartTask starts a task by key (rebuild_catalog_search_index for a stale main after a
	// failover). Nil in most tests.
	StartTask func(key string) error
}

// withDefaults returns o with every zero field set to its production value.
func (o SearchMirrorOptions) withDefaults() SearchMirrorOptions {
	defaultDuration := func(d *time.Duration, value time.Duration) {
		if *d <= 0 {
			*d = value
		}
	}
	defaultInt := func(n *int, value int) {
		if *n <= 0 {
			*n = value
		}
	}
	defaultDuration(&o.RunBudget, searchMirrorRunBudget)
	defaultDuration(&o.SettleQuiet, searchMirrorSettleQuiet)
	defaultDuration(&o.SettleWarnAfter, searchMirrorSettleWarnAfter)
	defaultDuration(&o.RetryBase, searchMirrorRetryBase)
	defaultDuration(&o.RetryMax, searchMirrorRetryMax)
	defaultInt(&o.MaxAttempts, searchMirrorMaxAttempts)
	defaultDuration(&o.DeadLetterRetryEvery, searchMirrorDeadLetterRetryEvery)
	defaultInt(&o.CopyQueueDepth, searchMirrorCopyQueueDepth)
	defaultInt(&o.MaxPayloadBytes, meilisearchMaxDocumentPayloadBytes)
	defaultDuration(&o.LeaseEvery, searchMirrorLeaseEvery)
	if o.MirrorLockKey == 0 {
		o.MirrorLockKey = pglock.Key(searchMirrorLockName)
	}
	if o.MainLockKey == 0 {
		o.MainLockKey = searchIndexMaintenanceLockID
	}
	if o.InstanceID == "" {
		o.InstanceID = nodeidentity.InstanceID()
	}
	if o.Host == "" {
		o.Host, _ = os.Hostname()
	}
	return o
}

// searchMirrorDocuments builds catalog search documents from Postgres, as the main index does.
type searchMirrorDocuments interface {
	LoadDocumentsByIDs(ctx context.Context, contentIDs []string, itemTypes []string, embedder string, semanticEnabled, binaryQuantized bool) ([]catalogSearchDocument, error)
	LoadDocumentsAfter(ctx context.Context, afterContentID string, limit int, itemTypes []string, embedder string, semanticEnabled, binaryQuantized bool) ([]catalogSearchDocument, error)
}

var _ searchMirrorDocuments = (*CatalogSearchIndexer)(nil)

// SearchMirror keeps the optional second Meilisearch in step with the catalog. cfg holds the
// mirror's API key: never log a SearchMirror or its cfg.
type SearchMirror struct {
	pool     *pgxpool.Pool
	cfg      SearchMirrorConfig
	settings SettingsStore         // live settings: page and batch sizes
	startup  CatalogSearchSettings // what this process built its main index for
	docs     searchMirrorDocuments
	opts     SearchMirrorOptions
	main     *meilisearchClient
	mirror   *meilisearchClient

	stoodDown atomic.Bool // another Silo copy took over search; this process stops mirroring
	startedAt time.Time   // this process's start, from the database clock on its first lease touch

	startedOnce      sync.Once   // sets startedAt once, from a run's first successful lease touch
	forceCopyPending atomic.Bool // ForceFullCopy asked for a copy that no run has recorded in the row yet
	offForProcess    atomic.Bool // a run found the mirror misconfigured; it stays off until Silo restarts

	runLock atomic.Pointer[pglock.Lock] // the mirror's lock while a run of this process holds it
}

// newSearchMirror builds a mirror; opts' zero fields take their production values.
func newSearchMirror(pool *pgxpool.Pool, cfg SearchMirrorConfig, settings SettingsStore, startup CatalogSearchSettings,
	docs searchMirrorDocuments, main, mirror *meilisearchClient, opts SearchMirrorOptions) *SearchMirror {
	return &SearchMirror{
		pool:     pool,
		cfg:      cfg,
		settings: settings,
		startup:  startup,
		docs:     docs,
		opts:     opts.withDefaults(),
		main:     main,
		mirror:   mirror,
	}
}

// searchMirrorLeaseTouchTimeout bounds one lease write, so a stuck database never stacks touches.
const searchMirrorLeaseTouchTimeout = 10 * time.Second

// NewSearchMirror builds the mirror for a configuration that is on and has no problem. It builds
// its own two Meilisearch clients the way (*CatalogSearchIndexer).loadClient builds the main
// index's: the main client from startup's address and key, the mirror client from cfg's, both with
// meilisearchIndexingTimeout. A process whose main search was not Meilisearch at start gets no main
// client; its runs wait with provider_not_meilisearch. docs builds the documents; in production it
// is the main index's *CatalogSearchIndexer. Errors never carry the mirror's key or credentials.
func NewSearchMirror(pool *pgxpool.Pool, cfg SearchMirrorConfig, settings SettingsStore, startup CatalogSearchSettings,
	docs searchMirrorDocuments, opts SearchMirrorOptions) (*SearchMirror, error) {
	switch {
	case pool == nil:
		return nil, errors.New("search mirror: no database")
	case !cfg.Active():
		return nil, errors.New("search mirror: not configured, or the configuration has a problem")
	case docs == nil:
		return nil, errors.New("search mirror: no catalog document source")
	}
	mirror, err := newMeilisearchClient(cfg.URL, cfg.APIKey, meilisearchIndexingTimeout)
	if err != nil {
		return nil, fmt.Errorf("search mirror: the mirror address %s is not usable", cfg.RedactedURL())
	}
	var main *meilisearchClient
	if startup.Provider == SearchProviderMeilisearch && strings.TrimSpace(startup.MeilisearchURL) != "" {
		// The raw error can quote the address, credentials included.
		if main, err = newMeilisearchClient(startup.MeilisearchURL, startup.MeilisearchAPIKey, meilisearchIndexingTimeout); err != nil {
			return nil, errors.New("search mirror: the main search address is not usable")
		}
	}
	startup.IndexTypes = slices.Clone(startup.IndexTypes)
	return newSearchMirror(pool, cfg, settings, startup, docs, main, mirror, opts), nil
}

// searchMirrorRunResult is what a run reports to its task.
type searchMirrorRunResult struct {
	Skipped string                `json:"skipped,omitempty"` // why the run did nothing: stood_down, misconfigured, another_run
	State   SearchMirrorStateName `json:"state,omitempty"`
	Reason  string                `json:"reason,omitempty"`
}

func searchMirrorRunResultOf(row searchMirrorRow) searchMirrorRunResult {
	return searchMirrorRunResult{State: row.State.State, Reason: row.State.Reason}
}

func (r searchMirrorRunResult) message(forcePending bool) string {
	switch r.Skipped {
	case "stood_down":
		return "Another Silo copy took over search; this server no longer runs the search mirror"
	case "misconfigured":
		return "The search mirror is misconfigured; it stays off until Silo restarts"
	case "another_run":
		if forcePending {
			return "Another search mirror run is under way; the full copy starts with a later run"
		}
		return "Another search mirror run is under way"
	}
	if r.Reason != "" {
		return fmt.Sprintf("Search mirror: %s (%s)", r.State, r.Reason)
	}
	return fmt.Sprintf("Search mirror: %s", r.State)
}

// RunOnce makes one mirror run (§2): it checks both servers' identities, compares the mirror with
// the main index's state row, then makes or continues a full copy, or applies the changes since
// the bookmark for up to RunBudget. It never takes the main index's lock, except inside the swap
// step after a failover. A run that finds another run took over the mirror stops quietly.
func (m *SearchMirror) RunOnce(ctx context.Context, progress SearchIndexProgressReporter) error {
	if m == nil {
		reportSearchIndexProgress(progress, 100, "The search mirror is not configured")
		return nil
	}
	result, err := m.run(ctx)
	if errors.Is(err, errSearchMirrorNotOwner) && ctx.Err() == nil {
		slog.InfoContext(ctx, "search mirror: another run took over the mirror; this run stopped", "component", "catalog")
		result, err = searchMirrorRunResult{Skipped: "another_run"}, nil
	}
	if err != nil {
		return err
	}
	setSearchIndexTaskResult(progress, result)
	reportSearchIndexProgress(progress, 100, result.message(m.forceCopyPending.Load()))
	return nil
}

// ForceFullCopy asks for a fresh full copy, then makes a run. The request stays with the process
// until a run holding the mirror's lock records it in the row (ForceCopy), so a run already under
// way, here or on another Silo copy, puts the copy off to a later run instead of losing it.
func (m *SearchMirror) ForceFullCopy(ctx context.Context, progress SearchIndexProgressReporter) error {
	if m == nil {
		reportSearchIndexProgress(progress, 100, "The search mirror is not configured")
		return nil
	}
	m.forceCopyPending.Store(true)
	return m.RunOnce(ctx, progress)
}

// StoodDown reports whether another Silo copy took over search, so this process no longer mirrors.
func (m *SearchMirror) StoodDown() bool {
	return m != nil && m.stoodDown.Load()
}

func (m *SearchMirror) run(ctx context.Context) (searchMirrorRunResult, error) {
	// 1. A process that stood down, or found the mirror misconfigured, does nothing more.
	if m.StoodDown() {
		return searchMirrorRunResult{Skipped: "stood_down"}, nil
	}
	if m.offForProcess.Load() {
		return searchMirrorRunResult{Skipped: "misconfigured"}, nil
	}

	// 2. One run at a time, across every Silo process sharing the database.
	lock, locked, err := pglock.TryAcquire(ctx, m.pool, m.opts.MirrorLockKey)
	if err != nil {
		return searchMirrorRunResult{}, fmt.Errorf("search mirror: take the mirror lock: %w", err)
	}
	if !locked {
		return searchMirrorRunResult{Skipped: "another_run"}, nil
	}
	m.runLock.Store(lock)
	defer func() {
		m.runLock.Store(nil)
		if err := lock.Release(ctx); err != nil {
			slog.WarnContext(ctx, "releasing the search mirror lock", "component", "catalog", "error", err)
		}
	}()
	return m.runLocked(ctx)
}

// runLocked is a run once it holds the mirror's lock.
func (m *SearchMirror) runLocked(ctx context.Context) (searchMirrorRunResult, error) {
	// 3. The lease. Its first insert, by StartProcessLease or here, fixes this process's start.
	startedAt, err := touchSearchMirrorProcess(ctx, m.pool, m.opts.InstanceID, m.opts.Host)
	if err != nil {
		return searchMirrorRunResult{}, fmt.Errorf("search mirror: %w", err)
	}
	m.startedOnce.Do(func() { m.startedAt = startedAt })

	// 4. The live settings, the main index's state and the mirror's own row.
	live, err := LoadCatalogSearchSettings(ctx, m.settings)
	if err != nil {
		return searchMirrorRunResult{}, fmt.Errorf("search mirror: load the search settings: %w", err)
	}
	main, err := NewSearchIndexEventRepository(m.pool).GetState(ctx, SearchProviderMeilisearch)
	if err != nil {
		return searchMirrorRunResult{}, fmt.Errorf("search mirror: read the main index state: %w", err)
	}
	row, _, err := loadSearchMirrorRow(ctx, m.pool)
	if err != nil {
		return searchMirrorRunResult{}, err
	}
	defer func() { m.latchMisconfigured(ctx, row) }()
	dirty, err := m.beginRun(ctx, &row)
	if err != nil {
		return searchMirrorRunResult{}, err
	}
	// Without a main Meilisearch there are no markers to read. Both the live provider and the
	// one this process started with must be Meilisearch: the main index follows the latter until
	// a restart.
	if live.Provider != SearchProviderMeilisearch || m.startup.Provider != SearchProviderMeilisearch || m.main == nil {
		return m.wait(ctx, &row, "provider_not_meilisearch")
	}

	// 5. Both servers' identities, and the failover steps that need no mirror server.
	if stop, err := m.identities(ctx, &row, main); stop || err != nil {
		return searchMirrorRunResultOf(row), err
	}

	// 6. Documents built for one schema must not go into an index made for another: wait while
	// the main index is not current.
	expectedSchema := catalogSearchMeilisearchSchemaVersion(m.startup.Embedder, m.startup.IndexTypes, m.startup.SemanticEnabled, m.startup.BinaryQuantized)
	switch {
	case strings.TrimSpace(main.ActiveIndexUID) == "":
		return m.wait(ctx, &row, "no_index")
	case catalogSearchIndexStateRequiresRebuild(main, expectedSchema, m.startup.MeilisearchIndex):
		return m.wait(ctx, &row, "main_schema_changing")
	}
	if row.State.State == SearchMirrorWaiting {
		row.State.State, row.State.Reason = searchMirrorResumedState(row), ""
		dirty = true
	}

	// 7. The lag after which the mirror makes a full copy.
	maxLag, err := searchMirrorEffectiveMaxLag(ctx, m.pool, m.cfg.MaxLag)
	if err != nil {
		return searchMirrorRunResultOf(row), fmt.Errorf("search mirror: %w", err)
	}
	if seconds := int64(maxLag / time.Second); row.State.MaxLagSeconds != seconds {
		row.State.MaxLagSeconds = seconds
		dirty = true
	}
	if dirty {
		if err := saveSearchMirrorRow(ctx, m.pool, &row); err != nil {
			return searchMirrorRunResultOf(row), err
		}
	}

	// 8. A full copy, started or continued, or else the changes since the bookmark.
	err = m.copyOrFollow(ctx, &row, main, maxLag)
	return searchMirrorRunResultOf(row), err
}

// beginRun prepares the row a run loaded. A misconfigured state that this process did not find
// was left by an earlier start: the configuration is read once per start, so a restart is how
// an operator fixes it. It is tried afresh; the problem is found again if it is still there. A
// ForceFullCopy request is recorded in the row. dirty says the row changed and is not saved.
func (m *SearchMirror) beginRun(ctx context.Context, row *searchMirrorRow) (dirty bool, err error) {
	if row.State.State == SearchMirrorMisconfigured {
		row.State.State, row.State.Reason = searchMirrorResumedState(*row), ""
		row.State.LastError, row.State.ErrorSince = "", nil
		dirty = true
	}
	if m.forceCopyPending.Swap(false) {
		row.State.ForceCopy = true
		if err := saveSearchMirrorRow(ctx, m.pool, row); err != nil {
			m.forceCopyPending.Store(true)
			return false, err
		}
		dirty = false
	}
	return dirty, nil
}

// identities is step 5: it reads both servers' identity markers and acts on the verdict. The
// failover steps need no mirror server, so they run whatever the mirror's marker said: the swap
// (a main showing the remembered mirror's id is enough), the settle replay after a swap, and a
// main rebuild a swap left owed. stop says the run ends here.
func (m *SearchMirror) identities(ctx context.Context, row *searchMirrorRow, main SearchIndexState) (stop bool, err error) {
	verdict, err := m.settleTargets(ctx, row)
	if errors.Is(err, errSearchMirrorSameServer) {
		// The same-server proof failed: settleTargets saved misconfigured/same_server. That is a
		// finding, not a failure to record, so it never reaches handleRunError;
		// latchMisconfigured turns the mirror off.
		return true, nil
	}
	if verdict == verdictSwapped {
		return true, m.swap(ctx, row)
	}
	if err := m.settle(ctx, row); err != nil {
		return true, err
	}
	if err := m.startOwedMainRebuild(ctx, row, main); err != nil {
		return true, err
	}
	switch {
	case verdict == verdictMirrorDown:
		// An unreadable mirror marker comes back with the marker's error: nothing is applied.
		return true, m.mirrorDown(ctx, row, err)
	case err != nil:
		// An unreadable main marker, with nothing written: an error to record.
		return true, m.runFailed(ctx, row, err)
	}
	// Markers adopted or written (and ForceCopy perhaps set) were saved by settleTargets.
	return false, nil
}

// copyOrFollow is the run's last steps:
//   - a full copy is started when one is needed (searchMirrorNeedsCopy), and a copy under way is
//     continued; no small changes go anywhere meanwhile (§5), and publishing clears the dead
//     letters from before it;
//   - otherwise the changes since the bookmark are applied, and then, on a mirror that is
//     following, the dead letters are retried once an hour.
func (m *SearchMirror) copyOrFollow(ctx context.Context, row *searchMirrorRow, main SearchIndexState, maxLag time.Duration) error {
	now := time.Now()
	deadline := now.Add(m.opts.RunBudget)
	if reason, needed := searchMirrorNeedsCopy(*row, main, now, maxLag); needed {
		if err := m.startCopy(ctx, row, main, reason); err != nil {
			return m.runFailed(ctx, row, err)
		}
	}
	if row.State.Copy != nil {
		if _, err := m.continueCopy(ctx, row, deadline); err != nil {
			return m.runFailed(ctx, row, err)
		}
		return nil
	}
	if err := m.follow(ctx, row, deadline); err != nil {
		return err
	}
	if row.State.State == SearchMirrorFollowing {
		return m.retryDeadLetters(ctx, row)
	}
	return nil
}

// checkOwner confirms, before a write to a Meilisearch server, that this run still holds the
// mirror's lock: a database failover closes the lock's session, which drops the lock, and the
// run that takes it next must not race this one. Outside a run it does nothing.
func (m *SearchMirror) checkOwner(ctx context.Context) error {
	lock := m.runLock.Load()
	if lock == nil {
		return nil
	}
	if err := lock.Check(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %w", errSearchMirrorNotOwner, err)
	}
	return nil
}

// searchMirrorResumedState is the state a mirror goes back to once nothing parks it: copying
// while a copy is under way, following otherwise.
func searchMirrorResumedState(row searchMirrorRow) SearchMirrorStateName {
	if row.State.Copy != nil {
		return SearchMirrorCopying
	}
	return SearchMirrorFollowing
}

// wait parks the mirror while the main index is not current, applying nothing (§2).
func (m *SearchMirror) wait(ctx context.Context, row *searchMirrorRow, reason string) (searchMirrorRunResult, error) {
	row.State.State, row.State.Reason = SearchMirrorWaiting, reason
	if err := saveSearchMirrorRow(ctx, m.pool, row); err != nil {
		return searchMirrorRunResult{}, err
	}
	return searchMirrorRunResultOf(*row), nil
}

// runFailed records a failure of the identity check or of a copy. Every Postgres error that
// reaches it is tagged database by the step that met it (searchMirrorDatabaseError), so
// handleRunError never mistakes one for a Meilisearch rejection. A shutdown, or a run that lost
// the mirror, records nothing.
func (m *SearchMirror) runFailed(ctx context.Context, row *searchMirrorRow, err error) error {
	if searchMirrorNotRecorded(ctx, err) {
		return err
	}
	return m.handleRunError(ctx, row, err)
}

// mirrorDown records that the mirror's identity marker could not be read (markerErr): the mirror
// applies nothing. A rejected key goes through handleRunError, which makes it a misconfiguration
// rather than an outage. A shutdown records nothing.
func (m *SearchMirror) mirrorDown(ctx context.Context, row *searchMirrorRow, markerErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	err := markerErr
	if err == nil {
		err = errors.New("search mirror: the mirror's identity marker could not be read")
	}
	if classifySearchMirrorError(err) == searchMirrorErrKeyRejected {
		return m.handleRunError(ctx, row, err)
	}
	return m.unreachable(ctx, row, err)
}

// latchMisconfigured turns the mirror off for the rest of this process once a run found it
// misconfigured (a rejected key, or a mirror address that reaches the main server), and logs that
// once. Runs before the latch reset a misconfigured state left by an earlier start, so a state
// still misconfigured here is this run's finding.
func (m *SearchMirror) latchMisconfigured(ctx context.Context, row searchMirrorRow) {
	if row.State.State != SearchMirrorMisconfigured || !m.offForProcess.CompareAndSwap(false, true) {
		return
	}
	cfg := m.cfg
	cfg.Problem = SearchMirrorProblem(row.State.Reason)
	slog.ErrorContext(ctx, "search mirror misconfigured; it stays off until Silo restarts", "component", "catalog",
		"problem", string(cfg.Problem), "detail", cfg.LogMessage())
}

// StartProcessLease writes this process's lease now, then every LeaseEvery until ctx ends. The
// settle replay reads the leases to learn when every Silo process older than a role swap has
// stopped. A process that stood down keeps its lease: its main sync still runs.
func (m *SearchMirror) StartProcessLease(ctx context.Context) {
	if m == nil || m.pool == nil {
		return
	}
	failing := false
	touch := func() {
		touchCtx, cancel := context.WithTimeout(ctx, searchMirrorLeaseTouchTimeout)
		defer cancel()
		_, err := touchSearchMirrorProcess(touchCtx, m.pool, m.opts.InstanceID, m.opts.Host)
		switch {
		case err == nil:
			failing = false
		case ctx.Err() != nil:
		case !failing: // one line per outage, not one per touch
			failing = true
			slog.WarnContext(ctx, "search mirror process lease not written", "component", "catalog", "error", err)
		}
	}
	touch()
	go func() {
		ticker := time.NewTicker(m.opts.LeaseEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				touch()
			}
		}
	}()
}

// searchMirrorProcessRuntime is this process's mirror, as wiring recorded it.
var searchMirrorProcessRuntime struct {
	mu     sync.RWMutex
	cfg    SearchMirrorConfig
	mirror *SearchMirror
}

// SetSearchMirrorRuntime records this process's mirror config and worker (nil when not running).
func SetSearchMirrorRuntime(cfg SearchMirrorConfig, m *SearchMirror) {
	searchMirrorProcessRuntime.mu.Lock()
	defer searchMirrorProcessRuntime.mu.Unlock()
	searchMirrorProcessRuntime.cfg = cfg
	searchMirrorProcessRuntime.mirror = m
}

// searchMirrorRuntime returns what SetSearchMirrorRuntime recorded; the zero config
// (Configured false) and nil when it was never called. Read by Tasks 12 and 13.
func searchMirrorRuntime() (SearchMirrorConfig, *SearchMirror) {
	searchMirrorProcessRuntime.mu.RLock()
	defer searchMirrorProcessRuntime.mu.RUnlock()
	return searchMirrorProcessRuntime.cfg, searchMirrorProcessRuntime.mirror
}
