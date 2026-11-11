package catalog

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
	"github.com/Silo-Server/silo-server/internal/nodeidentity"
	"github.com/jackc/pgx/v5/pgxpool"
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
