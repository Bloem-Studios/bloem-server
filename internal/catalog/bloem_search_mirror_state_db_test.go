package catalog

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
)

// fullSearchMirrorRow is a mirror row with every field set. Column times are whole
// microseconds in UTC, as Postgres keeps them and loadSearchMirrorRow returns them.
func fullSearchMirrorRow() searchMirrorRow {
	at := func(minutes int) *time.Time {
		v := time.Date(2026, 11, 11, 9, minutes, 7, 123456000, time.UTC)
		return &v
	}
	return searchMirrorRow{
		State: searchMirrorState{
			State:      SearchMirrorCopying,
			Reason:     "first_copy",
			Bookmark:   "100:120:105,118",
			BookmarkAt: at(1),
			Window:     &searchMirrorWindow{Upto: "120:130:", UptoAt: *at(2), AfterTx: 121, AfterID: 4567},
			Copy: &searchMirrorCopy{
				SideUID: "silo_media_items_mirrorcopy_1794390000", TargetUID: "silo_media_items",
				SchemaVersion: 3000123, Start: "130:131:", StartAt: *at(3), AfterContentID: "m0042",
				Done: 42, Total: 100, StartedAt: *at(4), ProgressAt: *at(12),
			},
			Retry:             &searchMirrorRetry{Attempts: 3, NextAt: *at(5), LastError: "refused"},
			Swap:              &searchMirrorSwap{At: *at(6), By: "8a6e0804-2bd0-4672-b79f-d3e1d3f1a1a1", Settling: true},
			MainTarget:        "main-id",
			MirrorTarget:      "mirror-id",
			LastError:         "mirror unreachable",
			ErrorSince:        at(7),
			LastSuccessAt:     at(8),
			DeadLetterRetryAt: at(9),
			MaxLagSeconds:     259200,
			Errors:            map[string]int64{"unreachable": 2, "rejected": 1, "database": 4},
			RoleSwaps:         1,
			ForceCopy:         true,
			RebuildMain:       "silo_media_items_rebuild_1",
			Gen:               7,
		},
		ActiveIndexUID: "silo_media_items",
		SchemaVersion:  3000123,
		DocumentCount:  99,
		LastRebuildAt:  at(10),
		LastSyncAt:     at(11),
	}
}

// requireEveryFieldSet fails for each zero field in v, following pointers and nested structs,
// so the round trip keeps covering fields added later.
func requireEveryFieldSet(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			t.Errorf("%s is nil", path)
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct || v.Type() == reflect.TypeFor[time.Time]() {
		if v.IsZero() {
			t.Errorf("%s is zero", path)
		}
		return
	}
	for i := range v.NumField() {
		requireEveryFieldSet(t, v.Field(i), path+"."+v.Type().Field(i).Name)
	}
}

func TestSearchMirrorStateRoundTrip(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	row := fullSearchMirrorRow()
	requireEveryFieldSet(t, reflect.ValueOf(row), "row")

	// The first save of a row that does not exist is from generation 0; each save moves it on.
	row.State.Gen = 0
	if err := saveSearchMirrorRow(t.Context(), e.Pool, &row); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := loadSearchMirrorRow(t.Context(), e.Pool)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, row) {
		t.Fatalf("round trip changed the row:\n got %+v\nwant %+v", got, row)
	}

	// A second save replaces the row; nothing of the first survives.
	next := searchMirrorRow{State: searchMirrorState{State: SearchMirrorFollowing, Bookmark: "140:140:", Gen: row.State.Gen}, ActiveIndexUID: "silo_media_items_rebuild_2"}
	if err := saveSearchMirrorRow(t.Context(), e.Pool, &next); err != nil {
		t.Fatalf("save again: %v", err)
	}
	got, ok, err = loadSearchMirrorRow(t.Context(), e.Pool)
	if err != nil || !ok {
		t.Fatalf("load again: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, next) {
		t.Fatalf("second save:\n got %+v\nwant %+v", got, next)
	}

	// The state is snake_case JSON in the bloem_mirror column of provider meilisearch_mirror.
	var bookmark string
	err = e.Pool.QueryRow(t.Context(), `SELECT bloem_mirror->>'bookmark' FROM catalog_search_index_state WHERE provider = $1`,
		SearchProviderMeilisearchMirror).Scan(&bookmark)
	if err != nil || bookmark != "140:140:" {
		t.Fatalf("bloem_mirror->>'bookmark' = %q, %v; want 140:140:", bookmark, err)
	}
}

func TestSearchMirrorStateAbsentRow(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	got, ok, err := loadSearchMirrorRow(t.Context(), e.Pool)
	if err != nil || ok {
		t.Fatalf("empty table: ok=%v err=%v row=%+v; want ok=false, nil error", ok, err, got)
	}
	// The main index's row is not the mirror's.
	e.SetMainState(t, e.Prefix)
	if _, ok, err := loadSearchMirrorRow(t.Context(), e.Pool); err != nil || ok {
		t.Fatalf("only the main row: ok=%v err=%v; want ok=false, nil error", ok, err)
	}
}

func TestSearchMirrorStateRestoreDeletesIt(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	row := fullSearchMirrorRow()
	row.State.Gen = 0
	if err := saveSearchMirrorRow(t.Context(), e.Pool, &row); err != nil {
		t.Fatalf("save: %v", err)
	}
	// internal/backup/interrupt.go's statement for this table, run by every restore.
	if _, err := e.Pool.Exec(t.Context(), `DELETE FROM catalog_search_index_state WHERE $1 <> ''`, "x"); err != nil {
		t.Fatalf("restore delete: %v", err)
	}
	if _, ok, err := loadSearchMirrorRow(t.Context(), e.Pool); err != nil || ok {
		t.Fatalf("after restore: ok=%v err=%v; want ok=false, nil error", ok, err)
	}
}

func TestSearchMirrorStateHold(t *testing.T) {
	bookmarkAt := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	startAt := bookmarkAt.Add(time.Hour)
	for _, tc := range []struct {
		name     string
		state    searchMirrorState
		snapshot string
		at       time.Time
		ok       bool
	}{
		{"nothing yet", searchMirrorState{}, "", time.Time{}, false},
		{"following", searchMirrorState{Bookmark: "10:20:", BookmarkAt: &bookmarkAt}, "10:20:", bookmarkAt, true},
		{"copying holds the copy's start, not the old bookmark",
			searchMirrorState{Bookmark: "10:20:", BookmarkAt: &bookmarkAt, Copy: &searchMirrorCopy{Start: "30:31:", StartAt: startAt}},
			"30:31:", startAt, true},
		{"first copy", searchMirrorState{Copy: &searchMirrorCopy{Start: "30:31:", StartAt: startAt}}, "30:31:", startAt, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, at, ok := tc.state.hold()
			if snapshot != tc.snapshot || !at.Equal(tc.at) || ok != tc.ok {
				t.Fatalf("hold() = %q, %v, %v; want %q, %v, %v", snapshot, at, ok, tc.snapshot, tc.at, tc.ok)
			}
		})
	}
}

func TestPgSnapshotParseAndFormat(t *testing.T) {
	for _, tc := range []struct {
		text string
		want pgSnapshot
	}{
		{"10:20:12,15", pgSnapshot{Xmin: 10, Xmax: 20, Xip: []uint64{12, 15}}},
		{"10:10:", pgSnapshot{Xmin: 10, Xmax: 10}},
		{"18446744073709551614:18446744073709551615:", pgSnapshot{Xmin: 1<<64 - 2, Xmax: 1<<64 - 1}},
	} {
		got, err := parsePgSnapshot(tc.text)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.text, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("parse %q = %+v, want %+v", tc.text, got, tc.want)
		}
		if got.String() != tc.text {
			t.Fatalf("format %+v = %q, want %q", got, got.String(), tc.text)
		}
	}
	for _, garbage := range []string{
		"", "garbage", "10:20", "10:20:12,15:", "x:20:", "10:x:", "10:20:x", "-1:20:", " 10:20:",
		"0:0:",                    // xid 0 is invalid
		"20:10:",                  // xmax before xmin
		"10:20:9",                 // in progress before xmin
		"10:20:20",                // in progress at xmax
		"10:20:15,12",             // out of order
		"10:20:12,,15",            // empty entry
		"10:20:12,",               // trailing comma
		"10:20:12,12",             // repeated
		"10:20:12 ",               // trailing space
		"1:18446744073709551616:", // past uint64
	} {
		if got, err := parsePgSnapshot(garbage); err == nil {
			t.Errorf("parse %q = %+v, want an error", garbage, got)
		}
	}

	t.Run("postgres", func(t *testing.T) {
		pool := newTestPool(t)
		// A snapshot with transactions in progress, as Postgres writes it, and Postgres reading
		// ours back unchanged.
		for _, text := range []string{"10:20:12,15", "10:10:"} {
			var echoed string
			if err := pool.QueryRow(t.Context(), `SELECT $1::text::pg_snapshot::text`, text).Scan(&echoed); err != nil {
				t.Fatalf("postgres reads %q: %v", text, err)
			}
			if echoed != text {
				t.Fatalf("postgres writes %q as %q", text, echoed)
			}
		}
		var current string
		if err := pool.QueryRow(t.Context(), `SELECT pg_current_snapshot()::text`).Scan(&current); err != nil {
			t.Fatalf("current snapshot: %v", err)
		}
		snapshot, err := parsePgSnapshot(current)
		if err != nil || snapshot.String() != current {
			t.Fatalf("current snapshot %q: parsed %+v (%q), %v", current, snapshot, snapshot.String(), err)
		}
	})
}

func TestSearchMirrorProcessLeaseKeepsStartTime(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	id := uuid.NewString()
	lease := func() (startedAt, heartbeatAt time.Time, mainTarget string) {
		t.Helper()
		err := e.Pool.QueryRow(t.Context(), `SELECT started_at, heartbeat_at, main_target FROM bloem_search_mirror_processes WHERE instance_id = $1`, id).
			Scan(&startedAt, &heartbeatAt, &mainTarget)
		if err != nil {
			t.Fatalf("read lease: %v", err)
		}
		return startedAt, heartbeatAt, mainTarget
	}

	// Before the process has read its main's marker, the lease reports no main.
	first, err := touchSearchMirrorProcess(t.Context(), e.Pool, id, "host-a", "")
	if err != nil {
		t.Fatalf("first touch: %v", err)
	}
	started, heartbeat, mainTarget := lease()
	if !first.Equal(started) {
		t.Fatalf("first touch returned %v, row has started_at %v", first, started)
	}
	if mainTarget != "" {
		t.Fatalf("first touch main_target = %q, want empty", mainTarget)
	}
	var host string
	if err := e.Pool.QueryRow(t.Context(), `SELECT host FROM bloem_search_mirror_processes WHERE instance_id = $1`, id).Scan(&host); err != nil || host != "host-a" {
		t.Fatalf("host = %q, %v; want host-a", host, err)
	}

	// A later touch records the main it now reports, and keeps the start.
	second, err := touchSearchMirrorProcess(t.Context(), e.Pool, id, "host-a", "main-a")
	if err != nil {
		t.Fatalf("second touch: %v", err)
	}
	started2, heartbeat2, mainTarget2 := lease()
	if !second.Equal(first) || !started2.Equal(started) {
		t.Fatalf("second touch moved the start: returned %v, row %v; want %v", second, started2, first)
	}
	if !heartbeat2.After(heartbeat) {
		t.Fatalf("heartbeat_at %v did not advance past %v", heartbeat2, heartbeat)
	}
	if mainTarget2 != "main-a" {
		t.Fatalf("second touch main_target = %q, want main-a", mainTarget2)
	}

	// The start stays the first insert's even when it is long past.
	if _, err := e.Pool.Exec(t.Context(), `UPDATE bloem_search_mirror_processes SET started_at = started_at - interval '1 hour' WHERE instance_id = $1`, id); err != nil {
		t.Fatalf("age start: %v", err)
	}
	third, err := touchSearchMirrorProcess(t.Context(), e.Pool, id, "host-a", "main-b")
	if err != nil {
		t.Fatalf("third touch: %v", err)
	}
	if want := first.Add(-time.Hour); !third.Equal(want) {
		t.Fatalf("third touch returned %v, want the aged start %v", third, want)
	}
	if _, _, mainTarget3 := lease(); mainTarget3 != "main-b" {
		t.Fatalf("third touch main_target = %q, want main-b", mainTarget3)
	}
}

// The lease loop reports the main's identity marker in the lease, and a marker that can't be
// read keeps the last one: the main has not changed because it is down for a moment.
func TestSearchMirrorProcessLeaseReportsMain(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	if err := e.Main.Client(t).writeTargetMarker(t.Context(), e.Prefix, "main-a"); err != nil {
		t.Fatalf("write the main's marker: %v", err)
	}
	m := e.NewMirror(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.StartProcessLease(ctx) // the first touch is written before it returns

	var mainTarget string
	err := e.Pool.QueryRow(t.Context(), `SELECT main_target FROM bloem_search_mirror_processes WHERE instance_id = $1`, m.opts.InstanceID).
		Scan(&mainTarget)
	if err != nil {
		t.Fatalf("read lease: %v", err)
	}
	if mainTarget != "main-a" {
		t.Fatalf("lease main_target = %q, want main-a", mainTarget)
	}

	e.Main.Stop()
	m.readMainTarget(t.Context())
	if got := m.seenMainTarget(); got != "main-a" {
		t.Fatalf("after a failed read the main target = %q, want main-a kept", got)
	}
}

func TestSearchMirrorOlderProcessAlive(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	var swapAt time.Time
	if err := e.Pool.QueryRow(t.Context(), `SELECT now()`).Scan(&swapAt); err != nil {
		t.Fatalf("now: %v", err)
	}
	swapBy := uuid.NewString()
	other := uuid.NewString()
	const quiet = 2 * time.Minute
	// After the swap the new main is the server that was the mirror; the old copy's processes
	// still read the old main's marker.
	const newMain, oldMain = "target-new-main", "target-old-main"

	for _, tc := range []struct {
		name       string
		instance   string
		started    string // relative to the swap
		heartbeat  string // relative to now
		mainTarget string // the main the lease reports
		want       bool
	}{
		{"started before the swap, no main reported yet", other, "-1 hour", "0", "", true},
		{"started before the swap, no main reported, quiet for 3 minutes", other, "-1 hour", "-3 minutes", "", false},
		{"the swapping instance itself", swapBy, "-1 hour", "0", "", false},
		{"started after the swap, no main reported yet", other, "1 second", "0", "", false},
		{"a new-copy process started before the swap that reports the new main", other, "-1 hour", "0", newMain, false},
		{"a process reporting the old main", other, "-1 hour", "0", oldMain, true},
		{"a process started after the swap that reports the old main", other, "1 second", "0", oldMain, true},
		{"a process reporting the old main, quiet for 3 minutes", other, "-1 hour", "-3 minutes", oldMain, false},
		{"the swapping instance reporting the old main", swapBy, "-1 hour", "0", oldMain, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := e.Pool.Exec(t.Context(), `DELETE FROM bloem_search_mirror_processes`); err != nil {
				t.Fatalf("clear: %v", err)
			}
			_, err := e.Pool.Exec(t.Context(), `
				INSERT INTO bloem_search_mirror_processes (instance_id, host, started_at, heartbeat_at, main_target)
				VALUES ($1, 'h', $2::timestamptz + $3::interval, now() + $4::interval, $5)`,
				tc.instance, swapAt, tc.started, tc.heartbeat, tc.mainTarget)
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
			got, err := searchMirrorOlderProcessAlive(t.Context(), e.Pool, swapAt, swapBy, newMain, quiet)
			if err != nil {
				t.Fatalf("older process alive: %v", err)
			}
			if got != tc.want {
				t.Fatalf("older process alive = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSearchMirrorMaxLagCappedWhileSwitching(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	tx, err := e.Pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()

	setSwitching := func(enabled bool) {
		t.Helper()
		if _, err := tx.Exec(t.Context(), `UPDATE public.bloem_silo_switching SET enabled = $1 WHERE singleton`, enabled); err != nil {
			t.Fatalf("set bloem_silo_switching %v: %v", enabled, err)
		}
	}
	check := func(configured, want time.Duration) {
		t.Helper()
		got, err := searchMirrorEffectiveMaxLag(t.Context(), tx, configured)
		if err != nil {
			t.Fatalf("effective max lag: %v", err)
		}
		if got != want {
			t.Fatalf("effective max lag for %v = %v, want %v", configured, got, want)
		}
	}

	setSwitching(false)
	check(72*time.Hour, 72*time.Hour)
	setSwitching(true)
	check(72*time.Hour, 20*time.Hour)
	check(12*time.Hour, 12*time.Hour)
}

func TestSearchMirrorTestEnvIsolatesSchemas(t *testing.T) {
	a := newSearchMirrorTestEnv(t)
	b := newSearchMirrorTestEnv(t)
	if a.Schema == b.Schema {
		t.Fatalf("both envs use schema %s", a.Schema)
	}
	a.Enqueue(t, nil, SearchIndexEventUpsert, "a1", "a2")
	b.Enqueue(t, b.Pool, SearchIndexEventDelete, "b1")
	b.Enqueue(t, b.Pool, SearchIndexEventRename, "b-old", "b-new")

	pending := func(e *searchMirrorTestEnv) []string {
		t.Helper()
		events, err := e.Events.ListPending(t.Context(), SearchProviderMeilisearch, 100)
		if err != nil {
			t.Fatalf("list pending in %s: %v", e.Schema, err)
		}
		var out []string
		for _, event := range events {
			out = append(out, event.Action+":"+event.PreviousContentID+">"+event.ContentID)
		}
		return out
	}
	if got, want := pending(a), []string{"upsert:>a1", "upsert:>a2"}; !slices.Equal(got, want) {
		t.Fatalf("env a sees %q, want %q", got, want)
	}
	if got, want := pending(b), []string{"delete:>b1", "rename:b-old>b-new"}; !slices.Equal(got, want) {
		t.Fatalf("env b sees %q, want %q", got, want)
	}
	for _, e := range []*searchMirrorTestEnv{a, b} {
		var untracked int
		if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM catalog_search_index_events WHERE tx_id IS NULL`).Scan(&untracked); err != nil || untracked != 0 {
			t.Fatalf("rows without tx_id in %s: %d, %v; want 0", e.Schema, untracked, err)
		}
	}
	var leaked int
	if err := a.Pool.QueryRow(t.Context(), `SELECT count(*) FROM public.catalog_search_index_events WHERE content_id = ANY($1)`,
		[]string{"a1", "a2", "b1", "b-new"}).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("rows in public: %d, %v; want 0", leaked, err)
	}

	// State rows are per env too, and SetMainState makes a main index the default settings accept.
	a.SetMainState(t, a.Prefix)
	state, err := a.Events.GetState(t.Context(), SearchProviderMeilisearch)
	if err != nil {
		t.Fatalf("a state: %v", err)
	}
	defaults := DefaultCatalogSearchSettings()
	version := catalogSearchMeilisearchSchemaVersion(defaults.Embedder, defaults.IndexTypes, defaults.SemanticEnabled, defaults.BinaryQuantized)
	if catalogSearchIndexStateRequiresRebuild(state, version, defaults.MeilisearchIndex) {
		t.Fatalf("after SetMainState the default settings still require a rebuild: %+v", state)
	}
	if !slices.Contains(a.Main.Indexes(), a.Prefix) || len(b.Main.Indexes()) != 0 {
		t.Fatalf("main indexes: a %q, b %q; want only a to hold %s", a.Main.Indexes(), b.Main.Indexes(), a.Prefix)
	}
	if state, err := b.Events.GetState(t.Context(), SearchProviderMeilisearch); err != nil || state.ActiveIndexUID != "" {
		t.Fatalf("b state = %+v, %v; want none", state, err)
	}

	// Lock keys are per env, so mirrors of different tests never contend.
	ma, mb := a.NewMirror(t), b.NewMirror(t)
	if ma.opts.MirrorLockKey == mb.opts.MirrorLockKey || ma.opts.MainLockKey == mb.opts.MainLockKey ||
		ma.opts.MainLockKey == searchIndexMaintenanceLockID || ma.opts.MirrorLockKey == pglock.Key(searchMirrorLockName) {
		t.Fatalf("lock keys are shared: a %d/%d, b %d/%d", ma.opts.MirrorLockKey, ma.opts.MainLockKey, mb.opts.MirrorLockKey, mb.opts.MainLockKey)
	}

	// The schema goes when its test ends.
	var schema string
	t.Run("env", func(t *testing.T) { schema = newSearchMirrorTestEnv(t).Schema })
	var exists bool
	if err := a.Pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&exists); err != nil || exists {
		t.Fatalf("schema %s exists after its test: %v, %v", schema, exists, err)
	}
}

func TestSearchMirrorTestEnvMainSyncMatchesDatabase(t *testing.T) {
	e := newSearchMirrorTestEnv(t)
	uid := e.Prefix
	e.SetMainState(t, uid)
	sync := e.MainSync(e.Main, uid)

	e.Docs.Put("m1", "One")
	e.Docs.Put("m2", "Two")
	e.Enqueue(t, nil, SearchIndexEventUpsert, "m1", "m2")
	if n := sync.Run(t.Context()); n != 2 {
		t.Fatalf("first run synced %d events, want 2", n)
	}
	e.AssertMatchesDatabase(t, e.Main, uid)

	// A rename and a delete; an upsert of an item that is gone deletes it.
	e.Docs.Remove("m2")
	e.Docs.Put("m3", "Three")
	e.Docs.Remove("m1")
	e.Enqueue(t, nil, SearchIndexEventRename, "m2", "m3")
	e.Enqueue(t, nil, SearchIndexEventUpsert, "m1")
	if n := sync.Run(t.Context()); n != 2 {
		t.Fatalf("second run synced %d events, want 2", n)
	}
	e.AssertMatchesDatabase(t, e.Main, uid)
	if n := sync.Run(t.Context()); n != 0 {
		t.Fatalf("idle run synced %d events, want 0", n)
	}

	// The assertion sees a difference.
	e.Docs.Put("m4", "Four")
	if diffs := e.differencesFromDatabase(e.Main, uid); len(diffs) != 1 || !strings.HasPrefix(diffs[0], "m4: missing") {
		t.Fatalf("differences = %q, want m4 missing", diffs)
	}
	if diffs := e.differencesFromDatabase(e.Mirror, uid); len(diffs) != 1 || diffs[0] != "the index does not exist" {
		t.Fatalf("differences on the empty mirror = %q", diffs)
	}

	// A database error fails the page: nothing is processed, every event spends a try.
	e.Enqueue(t, nil, SearchIndexEventUpsert, "m4")
	e.Docs.FailNext(errors.New("database is down"))
	if n := sync.Run(t.Context()); n != 0 {
		t.Fatalf("failing run synced %d events, want 0", n)
	}
	var attempts int
	var lastError string
	if err := e.Pool.QueryRow(t.Context(), `SELECT attempts, last_error FROM catalog_search_index_events WHERE content_id = 'm4'`).Scan(&attempts, &lastError); err != nil {
		t.Fatalf("read m4 event: %v", err)
	}
	if attempts != 1 || lastError != "database is down" {
		t.Fatalf("m4 event: attempts %d, last_error %q; want 1, database is down", attempts, lastError)
	}
}

func TestSearchMirrorTestEnvFakeDocumentsPageInContentIDOrder(t *testing.T) {
	d := newFakeMirrorDocuments()
	for _, id := range []string{"c", "a", "d", "b"} {
		d.Put(id, strings.ToUpper(id))
	}
	d.Remove("d")
	ids := func(docs []catalogSearchDocument) []string {
		var out []string
		for _, doc := range docs {
			out = append(out, doc.ContentID)
		}
		return out
	}
	defaults := DefaultCatalogSearchSettings()
	load := func(after string, limit int) []string {
		t.Helper()
		docs, err := d.LoadDocumentsAfter(t.Context(), after, limit, nil, defaults.Embedder, false, false)
		if err != nil {
			t.Fatalf("load after %q: %v", after, err)
		}
		return ids(docs)
	}
	if got := load("", 2); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("first page %q", got)
	}
	if got := load("b", 2); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("second page %q", got)
	}
	docs, err := d.LoadDocumentsByIDs(t.Context(), []string{"c", "x", "a", ""}, nil, defaults.Embedder, false, false)
	if err != nil || !slices.Equal(ids(docs), []string{"a", "c"}) {
		t.Fatalf("by ids = %q, %v; want a, c", ids(docs), err)
	}
	if want := catalogSearchMeilisearchSchemaVersion(defaults.Embedder, nil, false, false); docs[0].SchemaVersion != want || docs[0].Title != "A" {
		t.Fatalf("document %+v, want title A and schema version %d", docs[0], want)
	}

	failure := errors.New("boom")
	d.FailNext(failure)
	if _, err := d.LoadDocumentsByIDs(t.Context(), []string{"a"}, nil, defaults.Embedder, false, false); !errors.Is(err, failure) {
		t.Fatalf("after FailNext: %v, want boom", err)
	}
	if got := load("", 10); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("the failure is spent once: %q", got)
	}
}

func TestSearchMirrorOptionsDefaults(t *testing.T) {
	m := newSearchMirror(nil, SearchMirrorConfig{}, nil, DefaultCatalogSearchSettings(), nil, nil, nil, SearchMirrorOptions{})
	got := m.opts
	if got.StartTask != nil {
		t.Fatalf("StartTask defaults to %p, want nil", got.StartTask)
	}
	got.InstanceID, got.Host = "", "" // checked below
	want := SearchMirrorOptions{
		RunBudget:            5 * time.Minute,
		SettleQuiet:          2 * time.Minute,
		SettleWarnAfter:      30 * time.Minute,
		RetryBase:            30 * time.Second,
		RetryMax:             15 * time.Minute,
		MaxAttempts:          10,
		DeadLetterRetryEvery: time.Hour,
		CopyQueueDepth:       2,
		MaxPayloadBytes:      meilisearchMaxDocumentPayloadBytes,
		LeaseEvery:           15 * time.Second,
		MirrorLockKey:        pglock.Key("catalog.search_mirror"),
		MainLockKey:          searchIndexMaintenanceLockID,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults:\n got %+v\nwant %+v", got, want)
	}
	if _, err := uuid.Parse(m.opts.InstanceID); err != nil {
		t.Fatalf("InstanceID %q is not the process's uuid: %v", m.opts.InstanceID, err)
	}
	if m.stoodDown.Load() || !m.startedAt.IsZero() {
		t.Fatalf("a new mirror has stood down (%v) or a start time (%v)", m.stoodDown.Load(), m.startedAt)
	}

	// What a caller sets is kept.
	m = newSearchMirror(nil, SearchMirrorConfig{}, nil, DefaultCatalogSearchSettings(), nil, nil, nil,
		SearchMirrorOptions{RunBudget: time.Second, MaxAttempts: 3, InstanceID: "i", Host: "h", MainLockKey: 7})
	if m.opts.RunBudget != time.Second || m.opts.MaxAttempts != 3 || m.opts.InstanceID != "i" || m.opts.Host != "h" || m.opts.MainLockKey != 7 {
		t.Fatalf("set options were replaced: %+v", m.opts)
	}
}
