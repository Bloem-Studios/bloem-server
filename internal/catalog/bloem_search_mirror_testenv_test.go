package catalog

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
)

// The search mirror's test harness. Every mirror database test runs in its own schema that
// holds copies of the outbox, state, dead-letter and lease tables, with a pool whose
// search_path is "<schema>,public": Silo's unqualified SQL and real Postgres snapshots then
// run in isolation from every other test, and everything else (server_settings, the
// bloem_silo_switching toggle) still resolves to public.

// searchMirrorTestTables are the tables each test schema copies from public.
var searchMirrorTestTables = []string{
	"catalog_search_index_events",
	"catalog_search_index_state",
	"bloem_search_mirror_dead_letters",
	"bloem_search_mirror_processes",
}

type searchMirrorTestEnv struct {
	Pool   *pgxpool.Pool
	Schema string
	Events *SearchIndexEventRepository // latched to provider meilisearch
	Main   *fakeMeili
	Mirror *fakeMeili
	Docs   *fakeMirrorDocuments // the catalog: what LoadDocuments* returns
	Prefix string               // the index prefix, DefaultCatalogSearchSettings' MeilisearchIndex

	t *testing.T
	// settings is the live server_settings view the mirrors read: provider meilisearch, the
	// main's address and Prefix. Tests may change it before they run a mirror.
	settings *memSettings
}

// newSearchMirrorTestEnv makes a schema for the test, two fake Meilisearch servers and an
// empty catalog. It skips the test when SILO_TEST_DATABASE_URL is unset; the schema is dropped
// when the test ends.
func newSearchMirrorTestEnv(t *testing.T) *searchMirrorTestEnv {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	schema := fmt.Sprintf("mirror_t_%016x", rand.Uint64())
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse SILO_TEST_DATABASE_URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	// Cleanups run last-registered first: the fakes stop (releasing any request a test left
	// hanging), then the schema is dropped, then the pool closes.
	t.Cleanup(pool.Close)

	quoted := pgx.Identifier{schema}.Sanitize()
	statements := []string{"CREATE SCHEMA " + quoted}
	for _, table := range searchMirrorTestTables {
		statements = append(statements, fmt.Sprintf("CREATE TABLE %s.%s (LIKE public.%s INCLUDING ALL)",
			quoted, pgx.Identifier{table}.Sanitize(), pgx.Identifier{table}.Sanitize()))
	}
	err = pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
		for _, statement := range statements {
			if _, err := tx.Exec(t.Context(), statement); err != nil {
				return fmt.Errorf("%s: %w", statement, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("create test schema (run make test-db-prepare?): %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop test schema %s: %v", schema, err)
		}
	})

	e := &searchMirrorTestEnv{
		Pool:   pool,
		Schema: schema,
		Events: NewSearchIndexEventRepository(pool).WithActiveProvider(SearchProviderMeilisearch),
		Main:   newFakeMeili(t),
		Mirror: newFakeMeili(t),
		Docs:   newFakeMirrorDocuments(),
		Prefix: DefaultCatalogSearchSettings().MeilisearchIndex,
		t:      t,
	}
	e.settings = newMemSettings()
	e.settings.m[SearchSettingProvider] = SearchProviderMeilisearch
	e.settings.m[SearchSettingMeilisearchURL] = e.Main.URL()
	e.settings.m[SearchSettingMeilisearchIndex] = e.Prefix
	return e
}

// searchMirrorTestSchemaVersion is the schema version the default CatalogSearchSettings build:
// what SetMainState records and what AssertMatchesDatabase expects on every document.
func searchMirrorTestSchemaVersion() int {
	s := DefaultCatalogSearchSettings()
	return catalogSearchMeilisearchSchemaVersion(s.Embedder, s.IndexTypes, s.SemanticEnabled, s.BinaryQuantized)
}

// SetMainState records uid as the main index, as a finished main rebuild does: the provider
// meilisearch state row with the schema version the default settings build, so
// catalogSearchIndexStateRequiresRebuild is false for them when uid belongs to Prefix. It
// creates uid, empty, on e.Main.
func (e *searchMirrorTestEnv) SetMainState(t *testing.T, uid string) {
	t.Helper()
	if err := e.Events.UpdateStateAfterRebuild(t.Context(), SearchProviderMeilisearch, uid, searchMirrorTestSchemaVersion(), 0, 0); err != nil {
		t.Fatalf("set main state: %v", err)
	}
	e.Main.CreateIndexDirect(uid)
}

// Enqueue writes outbox rows through Silo's repository, on q (the pool when nil). action is
// upsert, delete or rename; a rename takes ids[0] as the previous content id and ids[1] as the
// new one. The repository is latched to provider meilisearch: the plain Enqueue helpers ask
// public.server_settings for the provider and would skip silently.
func (e *searchMirrorTestEnv) Enqueue(t *testing.T, q itemExecer, action string, ids ...string) {
	t.Helper()
	if q == nil {
		q = e.Pool
	}
	var err error
	switch action {
	case SearchIndexEventUpsert:
		err = e.Events.EnqueueUpserts(t.Context(), q, ids)
	case SearchIndexEventDelete:
		err = e.Events.EnqueueDeletes(t.Context(), q, ids)
	case SearchIndexEventRename:
		if len(ids) != 2 {
			t.Fatalf("enqueue rename: want previous and new content id, got %q", ids)
		}
		err = e.Events.EnqueueRename(t.Context(), q, ids[0], ids[1])
	default:
		t.Fatalf("enqueue: unknown action %q", action)
	}
	if err != nil {
		t.Fatalf("enqueue %s %q: %v", action, ids, err)
	}
}

// NewMirror builds a SearchMirror over the env: clients for e.Main and e.Mirror, e.Docs, the
// live settings, and lock keys unique to this env, so tests running at the same time never
// contend. Each mirror is its own process, with its own InstanceID. mutate runs before the
// production defaults fill what it leaves zero.
func (e *searchMirrorTestEnv) NewMirror(t *testing.T, mutate ...func(*SearchMirrorOptions)) *SearchMirror {
	t.Helper()
	opts := SearchMirrorOptions{
		MirrorLockKey: pglock.Key("bloem.search_mirror.test.mirror/" + e.Schema),
		MainLockKey:   pglock.Key("bloem.search_mirror.test.main/" + e.Schema),
		InstanceID:    uuid.NewString(),
		Host:          "search-mirror-test",
	}
	for _, m := range mutate {
		m(&opts)
	}
	startup, err := CatalogSearchSettingsFromMap(e.settings.m)
	if err != nil {
		t.Fatalf("search mirror test settings: %v", err)
	}
	cfg := SearchMirrorConfig{
		Configured: true,
		URL:        e.Mirror.URL(),
		KeySource:  searchMirrorKeySourceNone,
		MaxLag:     DefaultSearchMirrorMaxLag,
	}
	return newSearchMirror(e.Pool, cfg, e.settings, startup, e.Docs, e.Main.Client(t), e.Mirror.Client(t), opts)
}

// testMainSync is Silo's main sync (CatalogSearchIndexer.SyncOutbox) reduced to what the
// mirror's tests need: one page of pending events, coalesced, documents from e.Docs, deletes
// then adds on server's uid, each waited for, then the events marked processed. A Meilisearch
// failure marks the events failed, as Silo does; it is not a test failure.
type testMainSync struct {
	env    *searchMirrorTestEnv
	client *meilisearchClient
	uid    string
}

// MainSync is a main sync writing to server's uid. The client is made now, so it carries the
// key server requires at this moment.
func (e *searchMirrorTestEnv) MainSync(server *fakeMeili, uid string) *testMainSync {
	return &testMainSync{env: e, client: server.Client(e.t), uid: uid}
}

// Run syncs one page and returns how many events it marked processed: 0 when none were
// pending or the page failed. It may run on any goroutine.
func (s *testMainSync) Run(ctx context.Context) (n int) {
	e := s.env
	settings := DefaultCatalogSearchSettings()
	events, err := e.Events.ListPending(ctx, SearchProviderMeilisearch, settings.SyncBatchSize)
	if err != nil {
		e.t.Errorf("main sync: list pending: %v", err)
		return 0
	}
	if len(events) == 0 {
		return 0
	}
	ids := make([]int64, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	upsertIDs, deleteIDs := coalesceSearchIndexEvents(events)
	docs, err := e.Docs.LoadDocumentsByIDs(ctx, upsertIDs, settings.IndexTypes, settings.Embedder, settings.SemanticEnabled, settings.BinaryQuantized)
	if err != nil {
		s.markFailed(ctx, ids, err)
		return 0
	}
	found := make(map[string]bool, len(docs))
	for _, doc := range docs {
		found[doc.ContentID] = true
	}
	for _, id := range upsertIDs {
		if !found[id] {
			deleteIDs = append(deleteIDs, id)
		}
	}
	deleteIDs = compactNonEmptyStrings(deleteIDs)
	if len(deleteIDs) > 0 {
		ref, err := s.client.DeleteDocuments(ctx, s.uid, deleteIDs)
		if err == nil {
			err = s.client.WaitTask(ctx, ref)
		}
		if err != nil {
			s.markFailed(ctx, ids, err)
			return 0
		}
	}
	for _, batch := range catalogSearchDocumentPayloadBatches(docs, meilisearchMaxDocumentPayloadBytes) {
		ref, err := s.client.AddDocuments(ctx, s.uid, batch)
		if err == nil {
			err = s.client.WaitTask(ctx, ref)
		}
		if err != nil {
			s.markFailed(ctx, ids, err)
			return 0
		}
	}
	if err := e.Events.MarkProcessed(ctx, ids); err != nil {
		e.t.Errorf("main sync: mark processed: %v", err)
		return 0
	}
	return len(events)
}

func (s *testMainSync) markFailed(ctx context.Context, ids []int64, cause error) {
	if err := s.env.Events.MarkFailed(ctx, ids, cause); err != nil {
		s.env.t.Errorf("main sync: mark failed: %v", err)
	}
}

// AssertMatchesDatabase fails the test unless server's uid holds exactly e.Docs, document for
// document, each with the schema version the default settings build.
func (e *searchMirrorTestEnv) AssertMatchesDatabase(t *testing.T, server *fakeMeili, uid string) {
	t.Helper()
	if diffs := e.differencesFromDatabase(server, uid); len(diffs) > 0 {
		t.Errorf("index %s does not match the database:\n  %s", uid, strings.Join(diffs, "\n  "))
	}
}

// differencesFromDatabase lists how server's uid differs from e.Docs, in content-id order.
func (e *searchMirrorTestEnv) differencesFromDatabase(server *fakeMeili, uid string) []string {
	got := server.Documents(uid)
	if got == nil {
		return []string{"the index does not exist"}
	}
	want := e.Docs.all(searchMirrorTestSchemaVersion())
	var diffs []string
	for _, id := range slices.Sorted(maps.Keys(want)) {
		doc, ok := got[id]
		switch {
		case !ok:
			diffs = append(diffs, id+": missing")
		case !reflect.DeepEqual(doc, want[id]):
			diffs = append(diffs, fmt.Sprintf("%s: got %+v, want %+v", id, doc, want[id]))
		}
	}
	for _, id := range slices.Sorted(maps.Keys(got)) {
		if _, ok := want[id]; !ok {
			diffs = append(diffs, id+": not in the database")
		}
	}
	return diffs
}

// fakeMirrorDocuments is the catalog for the mirror's tests: what CatalogSearchIndexer's
// LoadDocuments* would build from Postgres. It is safe for concurrent use.
type fakeMirrorDocuments struct {
	mu       sync.Mutex
	docs     map[string]catalogSearchDocument
	failNext error
}

var _ searchMirrorDocuments = (*fakeMirrorDocuments)(nil)

func newFakeMirrorDocuments() *fakeMirrorDocuments {
	return &fakeMirrorDocuments{docs: map[string]catalogSearchDocument{}}
}

// Put adds or replaces the item id, a movie titled title.
func (d *fakeMirrorDocuments) Put(id, title string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.docs[id] = catalogSearchDocument{ContentID: id, Type: "movie", Title: title}
}

// Remove deletes the item id.
func (d *fakeMirrorDocuments) Remove(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.docs, id)
}

// FailNext makes the next LoadDocuments* call return err, as a database error would.
func (d *fakeMirrorDocuments) FailNext(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failNext = err
}

// LoadDocumentsByIDs returns the documents of the ids that exist, in content-id order, with the
// schema version the indexer would stamp. itemTypes does not filter.
func (d *fakeMirrorDocuments) LoadDocumentsByIDs(_ context.Context, contentIDs []string, itemTypes []string, embedder string, semanticEnabled, binaryQuantized bool) ([]catalogSearchDocument, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.takeFailureLocked(); err != nil {
		return nil, err
	}
	version := catalogSearchMeilisearchSchemaVersion(embedder, itemTypes, semanticEnabled, binaryQuantized)
	var out []catalogSearchDocument
	for _, id := range slices.Sorted(slices.Values(compactNonEmptyStrings(contentIDs))) {
		if doc, ok := d.docs[id]; ok {
			doc.SchemaVersion = version
			out = append(out, doc)
		}
	}
	return out, nil
}

// LoadDocumentsAfter returns up to limit documents with a content id after afterContentID, in
// content-id order, with the schema version the indexer would stamp. itemTypes does not filter.
func (d *fakeMirrorDocuments) LoadDocumentsAfter(_ context.Context, afterContentID string, limit int, itemTypes []string, embedder string, semanticEnabled, binaryQuantized bool) ([]catalogSearchDocument, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.takeFailureLocked(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	version := catalogSearchMeilisearchSchemaVersion(embedder, itemTypes, semanticEnabled, binaryQuantized)
	var out []catalogSearchDocument
	for _, id := range slices.Sorted(maps.Keys(d.docs)) {
		if id <= afterContentID {
			continue
		}
		if len(out) == limit {
			break
		}
		doc := d.docs[id]
		doc.SchemaVersion = version
		out = append(out, doc)
	}
	return out, nil
}

func (d *fakeMirrorDocuments) takeFailureLocked() error {
	err := d.failNext
	d.failNext = nil
	return err
}

// all returns every document by content id, stamped with schemaVersion.
func (d *fakeMirrorDocuments) all(schemaVersion int) map[string]catalogSearchDocument {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]catalogSearchDocument, len(d.docs))
	for id, doc := range d.docs {
		doc.SchemaVersion = schemaVersion
		out[id] = doc
	}
	return out
}
