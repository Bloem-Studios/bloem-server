package main

// Bloem-owned tests for bloemWireSearchMirror. Kept out of Silo's own test files so upstream
// merges do not conflict here; see contracts/seams.txt.
//
// The wiring runs for every operator, mirror or not: it replaces Silo's event cleanup step, and
// it must register nothing, log nothing and touch nothing while the mirror variables are unset.
// A mirror configured wrongly must cost one log line and never stop Silo.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/taskmanager"
	"github.com/Silo-Server/silo-server/internal/taskmanager/tasks"
)

const (
	wireCleanupKey = "cleanup_catalog_search_index_events"
	wireSyncKey    = "sync_catalog_search_mirror"
	wireRebuildKey = "rebuild_catalog_search_mirror"

	// Secrets the tests plant so a log line that quotes one is caught.
	wireKeySentinel     = "mirror-key-sentinel-4f1c9d"
	wireFileKeySentinel = "mirror-file-key-sentinel-8b2e71"
	wireURLPassSentinel = "url-password-sentinel-d03a5c"

	wireMirrorAddress = "http://127.0.0.1:7711"
	wireMainAddress   = "http://127.0.0.1:7710"
	// The main address with a login in it: a mirror named by the same string is the same address.
	wireMainAddressWithLogin = "http://user:" + wireURLPassSentinel + "@127.0.0.1:7710"

	wireUnreachableDSN = "postgres://silo:unused@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"
	wireLeaseTable     = "bloem_search_mirror_processes"
)

// wireLogStore collects what the process-wide logger receives.
type wireLogStore struct {
	mu      sync.Mutex
	records []wireLogRecord
}

type wireLogRecord struct {
	level slog.Level
	text  string // the message and every attribute, as one line
}

func (s *wireLogStore) add(r wireLogRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
}

func (s *wireLogStore) all() []wireLogRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]wireLogRecord(nil), s.records...)
}

func (s *wireLogStore) atLevel(level slog.Level) []wireLogRecord {
	var out []wireLogRecord
	for _, r := range s.all() {
		if r.level == level {
			out = append(out, r)
		}
	}
	return out
}

func (s *wireLogStore) dump() string {
	var b strings.Builder
	for _, r := range s.all() {
		fmt.Fprintf(&b, "\n  %s %s", r.level, r.text)
	}
	return b.String()
}

type wireLogHandler struct {
	store *wireLogStore
	attrs []slog.Attr
}

func (h *wireLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *wireLogHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Resolve())
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	h.store.add(wireLogRecord{level: r.Level, text: b.String()})
	return nil
}

func (h *wireLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &wireLogHandler{store: h.store, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *wireLogHandler) WithGroup(string) slog.Handler { return h }

// captureWireLogs swaps the process-wide logger for one that records, and puts the old one back
// when the test ends. Tests that use it must not run in parallel.
func captureWireLogs(t *testing.T) *wireLogStore {
	t.Helper()
	store := &wireLogStore{}
	previous := slog.Default()
	slog.SetDefault(slog.New(&wireLogHandler{store: store}))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return store
}

// wireStubTask stands for any other maintenance step.
type wireStubTask struct{ key string }

func (s *wireStubTask) Key() string                                                 { return s.key }
func (s *wireStubTask) Name() string                                                { return s.key }
func (s *wireStubTask) Description() string                                         { return s.key }
func (s *wireStubTask) Category() taskmanager.TaskCategory                          { return taskmanager.TaskCategorySystem }
func (s *wireStubTask) IsHidden() bool                                              { return false }
func (s *wireStubTask) DefaultTriggers() []taskmanager.TriggerConfig                { return nil }
func (s *wireStubTask) Execute(context.Context, taskmanager.ProgressReporter) error { return nil }

// wireMaintenanceSteps is the list main.go hands bloemWireSearchMirror: Silo's own event cleanup
// step, built the way main.go builds it, between two others.
func wireMaintenanceSteps() (steps []taskmanager.Task, silosCleanup taskmanager.Task, others []taskmanager.Task) {
	silosCleanup = tasks.NewCatalogSearchEventRetentionTask(catalog.NewSearchIndexEventRepository(nil))
	others = []taskmanager.Task{&wireStubTask{key: "wire_step_before"}, &wireStubTask{key: "wire_step_after"}}
	return []taskmanager.Task{others[0], silosCleanup, others[1]}, silosCleanup, others
}

func newWireTaskManager() *taskmanager.TaskManager {
	return taskmanager.New(nil, nil, nil, slog.New(slog.DiscardHandler))
}

// wireRegistered reports whether the manager has a task under key, hidden ones included.
func wireRegistered(m *taskmanager.TaskManager, key string) bool {
	for _, info := range m.ListTasks(true) {
		if info.Key == key {
			return true
		}
	}
	return false
}

// wireRetentionPrunerType is the type of the pruner inside Silo's event cleanup step. The field is
// unexported, and the step is the only thing that says which pruner it will run.
func wireRetentionPrunerType(t *testing.T, step taskmanager.Task) reflect.Type {
	t.Helper()
	retention, ok := step.(*tasks.CatalogSearchEventRetentionTask)
	if !ok {
		t.Fatalf("step %q is a %T, want *tasks.CatalogSearchEventRetentionTask", step.Key(), step)
	}
	field := reflect.ValueOf(retention).Elem().FieldByName("pruner")
	if !field.IsValid() || field.Kind() != reflect.Interface || field.IsNil() {
		t.Fatalf("the cleanup step has no pruner (field %v)", field)
	}
	return field.Elem().Type()
}

// assertCleanupStepReplaced checks that exactly one step, the one keyed
// cleanup_catalog_search_index_events, was replaced by the mirror-aware one, still in its place,
// and that every other step is the very same step.
func assertCleanupStepReplaced(t *testing.T, got []taskmanager.Task, silosCleanup taskmanager.Task, others []taskmanager.Task) {
	t.Helper()
	if len(got) != 3 {
		t.Fatalf("%d steps came back, want 3", len(got))
	}
	if got[0] != others[0] || got[2] != others[1] {
		t.Errorf("a step other than %s was replaced", wireCleanupKey)
	}
	replaced := 0
	for _, step := range got {
		if step.Key() == wireCleanupKey {
			replaced++
		}
	}
	if replaced != 1 {
		t.Fatalf("%d steps are keyed %s, want exactly 1", replaced, wireCleanupKey)
	}
	if got[1] == silosCleanup {
		t.Fatalf("the step keyed %s is still Silo's own; Silo may have renamed the key bloemWireSearchMirror replaces", wireCleanupKey)
	}
	if want, have := reflect.TypeFor[*catalog.SearchMirrorAwarePruner](), wireRetentionPrunerType(t, got[1]); have != want {
		t.Errorf("the cleanup step prunes with %v, want %v", have, want)
	}
	// The reflection reads what it should: Silo's own step prunes with Silo's repository.
	if want, have := reflect.TypeFor[*catalog.SearchIndexEventRepository](), wireRetentionPrunerType(t, silosCleanup); have != want {
		t.Errorf("Silo's own cleanup step prunes with %v, want %v", have, want)
	}
}

// setWireMirrorEnv sets the four mirror variables for the test, blank when env leaves one out.
func setWireMirrorEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, name := range []string{
		catalog.EnvSearchMirrorURL, catalog.EnvSearchMirrorAPIKeyFile,
		catalog.EnvSearchMirrorAPIKey, catalog.EnvSearchMirrorMaxLag,
	} {
		t.Setenv(name, env[name])
	}
}

// wireStartupSettings are the settings the process started with: Meilisearch as the main search.
func wireStartupSettings() catalog.CatalogSearchSettings {
	startup := catalog.DefaultCatalogSearchSettings()
	startup.Provider = catalog.SearchProviderMeilisearch
	startup.MeilisearchURL = wireMainAddress
	return startup
}

// wireSettingsStore is a SettingsStore over a map.
type wireSettingsStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *wireSettingsStore) Get(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key], nil
}

func (s *wireSettingsStore) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[key] = value
	return nil
}

func (s *wireSettingsStore) GetAll(context.Context) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out, nil
}

// wireMirrorMetricDescs are the descriptors of the mirror's collector while it is registered on
// the default registry; none when it is not.
func wireMirrorMetricDescs() []*prometheus.Desc {
	registry, ok := prometheus.DefaultRegisterer.(prometheus.Collector)
	if !ok {
		return nil
	}
	all := make(chan *prometheus.Desc)
	go func() {
		defer close(all)
		registry.Describe(all)
	}()
	var out []*prometheus.Desc
	for desc := range all {
		if strings.Contains(desc.String(), `fqName: "silo_search_mirror_`) {
			out = append(out, desc)
		}
	}
	return out
}

// wireMetricProbe describes the same metrics as the mirror's collector, so it registers only
// once that collector is off the registry.
type wireMetricProbe struct{ descs []*prometheus.Desc }

func (p wireMetricProbe) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range p.descs {
		ch <- desc
	}
}

func (p wireMetricProbe) Collect(chan<- prometheus.Metric) {}

// stopWireMirrorBackground ends what the wiring started in the background (the metrics sampler,
// the lease loop) and waits for the sampler to take its collector off the default registry, so
// the next test can register its own. It also forgets the process's mirror runtime.
func stopWireMirrorBackground(t *testing.T, cancel context.CancelFunc) {
	t.Helper()
	probe := wireMetricProbe{descs: wireMirrorMetricDescs()}
	cancel()
	catalog.SetSearchMirrorRuntime(catalog.SearchMirrorConfig{}, nil)
	if len(probe.descs) == 0 {
		return
	}
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		err := prometheus.Register(probe)
		if err == nil {
			prometheus.Unregister(probe)
			return
		}
		select {
		case <-timeout.C:
			t.Errorf("the search mirror's metrics collector was still registered 5 s after its context ended: %v", err)
			return
		case <-poll.C:
		}
	}
}

func TestBloemWireSearchMirrorOffRegistersNothing(t *testing.T) {
	setWireMirrorEnv(t, nil)
	logs := captureWireLogs(t)
	steps, silosCleanup, others := wireMaintenanceSteps()
	taskMgr := newWireTaskManager()

	// No pool and no indexer: the wiring must not need either while the mirror is off, and a
	// nil pool makes any database use a panic rather than a quiet success.
	got := bloemWireSearchMirror(t.Context(), taskMgr, nil, &wireSettingsStore{}, wireStartupSettings(), nil, steps)

	assertCleanupStepReplaced(t, got, silosCleanup, others)
	for _, key := range []string{wireSyncKey, wireRebuildKey} {
		if wireRegistered(taskMgr, key) {
			t.Errorf("task %s is registered with the mirror off", key)
		}
	}
	if n := len(taskMgr.ListTasks(true)); n != 0 {
		t.Errorf("%d tasks are registered with the mirror off, want none", n)
	}
	if records := logs.all(); len(records) != 0 {
		t.Errorf("the wiring logged with the mirror off:%s", logs.dump())
	}
}

// wireBadConfigCase is one way to set the mirror up wrongly, and the problem it must report.
type wireBadConfigCase struct {
	name    string
	env     map[string]string
	mainURL string // the main address when it is not wireMainAddress
	problem catalog.SearchMirrorProblem
}

// wireBadConfigCases are the five problems the settings can have at start, each with a secret
// planted where the wiring could leak it.
func wireBadConfigCases(t *testing.T) []wireBadConfigCase {
	t.Helper()
	keyFile := filepath.Join(t.TempDir(), "mirror.key")
	if err := os.WriteFile(keyFile, []byte(wireFileKeySentinel+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingFile := filepath.Join(t.TempDir(), "absent.key")

	return []wireBadConfigCase{
		{
			name: "invalid URL",
			env: map[string]string{
				catalog.EnvSearchMirrorURL:    "ftp://mirror.invalid:7711",
				catalog.EnvSearchMirrorAPIKey: wireKeySentinel,
			},
			problem: catalog.SearchMirrorProblemURL,
		},
		{
			name: "both keys",
			env: map[string]string{
				catalog.EnvSearchMirrorURL:        wireMirrorAddress,
				catalog.EnvSearchMirrorAPIKeyFile: keyFile,
				catalog.EnvSearchMirrorAPIKey:     wireKeySentinel,
			},
			problem: catalog.SearchMirrorProblemBothKeys,
		},
		{
			name: "unreadable key file",
			env: map[string]string{
				catalog.EnvSearchMirrorURL:        wireMirrorAddress,
				catalog.EnvSearchMirrorAPIKeyFile: missingFile,
			},
			problem: catalog.SearchMirrorProblemKeyFile,
		},
		{
			name: "same address as main",
			env: map[string]string{
				catalog.EnvSearchMirrorURL:    wireMainAddressWithLogin,
				catalog.EnvSearchMirrorAPIKey: wireKeySentinel,
			},
			mainURL: wireMainAddressWithLogin,
			problem: catalog.SearchMirrorProblemSameURL,
		},
		{
			name: "bad max lag",
			env: map[string]string{
				catalog.EnvSearchMirrorURL:    wireMirrorAddress,
				catalog.EnvSearchMirrorAPIKey: wireKeySentinel,
				catalog.EnvSearchMirrorMaxLag: "soon",
			},
			problem: catalog.SearchMirrorProblemMaxLag,
		},
	}
}

func TestBloemWireSearchMirrorBadConfigNeverStopsSilo(t *testing.T) {
	cases := wireBadConfigCases(t)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setWireMirrorEnv(t, tc.env)
			startup := wireStartupSettings()
			if tc.mainURL != "" {
				startup.MeilisearchURL = tc.mainURL
			}
			// The case must be the problem it names, or the test proves nothing.
			if have := catalog.LoadSearchMirrorConfig(os.Getenv, os.ReadFile, startup.MeilisearchURL).Problem; have != tc.problem {
				t.Fatalf("the environment gives problem %q, want %q", have, tc.problem)
			}

			logs := captureWireLogs(t)
			steps, silosCleanup, others := wireMaintenanceSteps()
			taskMgr := newWireTaskManager()

			// A pool that has never connected: a misconfigured mirror still starts its metrics,
			// whose sampler then fails to read and may warn, but never errors.
			ctx, cancel := context.WithCancel(t.Context())
			pool, err := pgxpool.New(ctx, wireUnreachableDSN)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			t.Cleanup(func() { stopWireMirrorBackground(t, cancel) })

			got := bloemWireSearchMirror(ctx, taskMgr, pool, &wireSettingsStore{}, startup, nil, steps)

			assertCleanupStepReplaced(t, got, silosCleanup, others)
			for _, key := range []string{wireSyncKey, wireRebuildKey} {
				if wireRegistered(taskMgr, key) {
					t.Errorf("task %s is registered although the mirror is misconfigured", key)
				}
			}
			errorLines := logs.atLevel(slog.LevelError)
			if len(errorLines) != 1 {
				t.Fatalf("%d error lines logged, want exactly 1:%s", len(errorLines), logs.dump())
			}
			if !strings.Contains(errorLines[0].text, string(tc.problem)) {
				t.Errorf("the error line does not name the problem %q: %s", tc.problem, errorLines[0].text)
			}
			for _, secret := range []string{wireKeySentinel, wireFileKeySentinel, wireURLPassSentinel} {
				if strings.Contains(logs.dump(), secret) {
					t.Errorf("a log line contains the secret %q:%s", secret, logs.dump())
				}
			}
		})
	}
}

// newWireSearchMirrorDatabase gives the test a pool on the test database whose search_path is a
// schema of its own, holding copies of the tables the mirror reads and writes, so the lease row
// the wiring writes never reaches public. It skips the test when SILO_TEST_DATABASE_URL is unset.
func newWireSearchMirrorDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	schema := "mirror_wire_" + randomSuffix(t)
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse SILO_TEST_DATABASE_URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)

	quoted := pgx.Identifier{schema}.Sanitize()
	statements := []string{"CREATE SCHEMA " + quoted}
	for _, table := range []string{
		"catalog_search_index_events", "catalog_search_index_state",
		"bloem_search_mirror_dead_letters", wireLeaseTable,
	} {
		name := pgx.Identifier{table}.Sanitize()
		statements = append(statements, fmt.Sprintf("CREATE TABLE %s.%s (LIKE public.%s INCLUDING ALL)", quoted, name, name))
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
	return pool
}

func TestBloemWireSearchMirrorConfiguredRegistersBoth(t *testing.T) {
	pool := newWireSearchMirrorDatabase(t)
	setWireMirrorEnv(t, map[string]string{
		catalog.EnvSearchMirrorURL:    wireMirrorAddress,
		catalog.EnvSearchMirrorAPIKey: wireKeySentinel,
	})
	logs := captureWireLogs(t)
	steps, silosCleanup, others := wireMaintenanceSteps()
	taskMgr := newWireTaskManager()
	startup := wireStartupSettings()
	settings := &wireSettingsStore{m: map[string]string{}}
	indexer := catalog.NewCatalogSearchIndexerFromSettings(pool, settings, startup)

	// Cleanups run last first: the background work stops, then the schema is dropped, then the
	// pool closes, then the logger is put back.
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { stopWireMirrorBackground(t, cancel) })

	got := bloemWireSearchMirror(ctx, taskMgr, pool, settings, startup, indexer, steps)

	assertCleanupStepReplaced(t, got, silosCleanup, others)
	for _, key := range []string{wireSyncKey, wireRebuildKey} {
		if !wireRegistered(taskMgr, key) {
			t.Errorf("task %s is not registered with the mirror configured", key)
		}
	}
	// The lease loop's first touch is synchronous: this process has a row by now.
	var leases int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM `+wireLeaseTable).Scan(&leases); err != nil {
		t.Fatal(err)
	}
	if leases != 1 {
		t.Errorf("%d process leases written, want 1", leases)
	}
	if errs := logs.atLevel(slog.LevelError); len(errs) != 0 {
		t.Errorf("error lines logged for a good configuration:%s", logs.dump())
	}
	if strings.Contains(logs.dump(), wireKeySentinel) {
		t.Errorf("a log line contains the mirror key:%s", logs.dump())
	}
}
