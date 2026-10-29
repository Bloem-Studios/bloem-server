package storagesource

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// scriptedListing serves fixed pages per directory. Incremental pages answer a
// request carrying a change token; reject names a cursor whose incremental
// request fails with FailedPrecondition, as an expired token does.
type scriptedListing struct {
	full, incremental map[string][][]*storagev1.Entry
	removed           map[string][]string
	reject            map[string]string
	tokens            bool
}

func (s *scriptedListing) list(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
	pages := s.full[q.DirectoryId]
	if q.ChangesSince != "" {
		if cursor, ok := s.reject[q.DirectoryId]; ok && cursor == q.Cursor {
			return nil, status.Error(codes.FailedPrecondition, "expired token")
		}
		pages = s.incremental[q.DirectoryId]
	}
	index := 0
	if q.Cursor != "" {
		n, err := strconv.Atoi(q.Cursor)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "cursor")
		}
		index = n
	}
	page := &storagev1.ListResponse{}
	if index < len(pages) {
		page.Entries = pages[index]
	}
	if index+1 < len(pages) {
		page.NextCursor = strconv.Itoa(index + 1)
	} else {
		page.Complete = true
		if q.ChangesSince != "" {
			page.RemovedEntryIds = s.removed[q.DirectoryId]
		}
		if s.tokens {
			page.ChangeToken = q.DirectoryId + "-T"
		}
	}
	return page, nil
}

func rows(t *testing.T, r *Repository, query string, args ...any) map[string]bool {
	t.Helper()
	result, err := r.pool.Query(t.Context(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(result, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	set := make(map[string]bool, len(out))
	for _, v := range out {
		set[v] = true
	}
	return set
}

// fullRebuildOracle evaluates, after each committed page, what rebuilding the
// whole run graph would publish: the reach is exactly the current closure,
// and published memberships and queued directories are everything any
// earlier page's closure reached.
type fullRebuildOracle struct {
	r                 *Repository
	lease             Lease
	members, queued   map[string]bool
	reachedMembership map[string]bool
	reachedDirs       map[string]bool
}

func newFullRebuildOracle(t *testing.T, r *Repository, lease Lease) *fullRebuildOracle {
	t.Helper()
	return &fullRebuildOracle{
		r: r, lease: lease,
		members:           rows(t, r, `SELECT media_file_id::text||'|'||root_id::text FROM bloem_storage_file_roots WHERE location_id=$1 AND enabled`, lease.LocationID),
		queued:            rows(t, r, `SELECT directory_id FROM bloem_storage_scan_directories WHERE run_id=$1`, lease.RunID),
		reachedMembership: map[string]bool{},
		reachedDirs:       map[string]bool{},
	}
}

func (o *fullRebuildOracle) check(t *testing.T, step string) {
	t.Helper()
	args := []any{o.lease.RunID, o.lease.LocationID, o.lease.ConfigurationRevision}
	reach := rows(t, o.r, locationReachSQL+`SELECT DISTINCT directory_id||'|'||root_id::text FROM reach`, args...)
	for key := range reach {
		dir, _, _ := cut(key)
		o.reachedDirs[dir] = true
	}
	maps.Copy(o.reachedMembership, rows(t, o.r, locationReachSQL+`SELECT DISTINCT f.media_file_id::text||'|'||r.root_id::text FROM reach r
 JOIN bloem_storage_scan_directories d ON d.run_id=$1 AND d.directory_id=r.directory_id AND d.listing_mode<>'pending'
 JOIN bloem_storage_location_edges e ON e.location_id=$2 AND e.parent_directory_id=r.directory_id
 JOIN bloem_storage_file_refs f ON f.location_id=e.location_id AND f.entry_id=e.entry_id
 JOIN bloem_storage_location_entries x ON x.location_id=f.location_id AND x.entry_id=f.entry_id AND x.kind=1 AND x.revision=f.revision AND x.configuration_revision=f.configuration_revision
 WHERE f.configuration_revision=$3`, args...))
	actualReach := rows(t, o.r, `SELECT directory_id||'|'||root_id::text FROM bloem_storage_scan_directory_roots WHERE run_id=$1`, o.lease.RunID)
	if !maps.Equal(actualReach, reach) {
		t.Fatalf("%s: reach %v, full rebuild %v", step, sorted(actualReach), sorted(reach))
	}
	wantMembers := maps.Clone(o.members)
	maps.Copy(wantMembers, o.reachedMembership)
	actualMembers := rows(t, o.r, `SELECT media_file_id::text||'|'||root_id::text FROM bloem_storage_file_roots WHERE location_id=$1 AND enabled`, o.lease.LocationID)
	if !maps.Equal(actualMembers, wantMembers) {
		t.Fatalf("%s: memberships %v, full rebuild %v", step, sorted(actualMembers), sorted(wantMembers))
	}
	wantQueued := maps.Clone(o.queued)
	maps.Copy(wantQueued, o.reachedDirs)
	actualQueued := rows(t, o.r, `SELECT directory_id FROM bloem_storage_scan_directories WHERE run_id=$1`, o.lease.RunID)
	if !maps.Equal(actualQueued, wantQueued) {
		t.Fatalf("%s: queued %v, full rebuild %v", step, sorted(actualQueued), sorted(wantQueued))
	}
}

func cut(s string) (string, string, bool) {
	for i := range len(s) {
		if s[i] == '|' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func sorted(set map[string]bool) []string { return slices.Sorted(maps.Keys(set)) }

// oracleRun scans to completion, checking every committed page against the
// full rebuild. It returns the completed lease. A staged run publishes each
// directory's files only on its completing page, as the audiobook stager does.
func oracleRun(t *testing.T, r *Repository, l Location, listing *scriptedListing, skip DirectorySkipper, staged bool) Lease {
	t.Helper()
	client := providerClient(t, listing.list)
	lease := scopedBegin(t, r, l)
	oracle := newFullRebuildOracle(t, r, lease)
	var calls int
	direct := scopedPublisher(t, l, &calls)
	pending := map[string][]*storagev1.Entry{}
	for steps := 0; steps < 200; steps++ {
		cp, more, err := r.NextDirectory(t.Context(), lease)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			if err = r.Complete(t.Context(), lease); err != nil {
				t.Fatal(err)
			}
			return lease
		}
		page, err := r.FetchPage(t.Context(), lease, cp, client)
		if errors.Is(err, ErrCheckpointRestart) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		publish := direct
		if staged {
			dir, complete := cp.DirectoryID, page.GetComplete()
			publish = func(ctx context.Context, tx pgx.Tx, files []*storagev1.Entry, removed []string) error {
				pending[dir] = append(pending[dir], files...)
				if !complete {
					return nil
				}
				all := pending[dir]
				delete(pending, dir)
				return direct(ctx, tx, all, removed)
			}
		}
		if err = r.applyPage(t.Context(), lease, cp, page, skip, nil, publish); err != nil {
			t.Fatal(err)
		}
		oracle.check(t, fmt.Sprintf("page %d of %s@%q", steps, cp.DirectoryID, cp.Cursor))
	}
	t.Fatal("directory graph did not terminate")
	return lease
}

// checkCompletedLocation verifies a completed run left exactly the completed
// graph's memberships enabled, and absence stamps on exactly the entries that
// graph no longer reaches.
func checkCompletedLocation(t *testing.T, r *Repository, l Location, lease Lease, step string) {
	t.Helper()
	want := rows(t, r, completedLocationReachSQL+`SELECT v.media_file_id::text||'|'||v.root_id::text FROM (`+completedMembershipSQL+`) v`, l.ID, lease.ConfigurationRevision)
	got := rows(t, r, `SELECT media_file_id::text||'|'||root_id::text FROM bloem_storage_file_roots WHERE location_id=$1 AND enabled`, l.ID)
	if !maps.Equal(got, want) {
		t.Fatalf("%s: completed memberships %v, want %v", step, sorted(got), sorted(want))
	}
	wrong := rows(t, r, completedLocationReachSQL+`SELECT e.entry_id FROM bloem_storage_location_entries e WHERE e.location_id=$1 AND
 (e.absence_confirmed_at IS NULL) <> EXISTS(SELECT 1 FROM bloem_storage_location_edges edge JOIN reach r ON r.directory_id=edge.parent_directory_id WHERE edge.location_id=e.location_id AND edge.entry_id=e.entry_id)`, l.ID, lease.ConfigurationRevision)
	if len(wrong) > 0 {
		t.Fatalf("%s: absence stamps wrong for %v", step, sorted(wrong))
	}
}

func books(ids ...string) []*storagev1.Entry {
	out := make([]*storagev1.Entry, 0, len(ids))
	for _, id := range ids {
		out = append(out, bookEntry(id))
	}
	return out
}

func page(entries ...*storagev1.Entry) []*storagev1.Entry { return entries }

// Per-page propagation must reach exactly what a full rebuild of the run graph
// would, across aliases, cycles, roots reached late, shrinking incremental
// listings, skipped directories, demoted and promoted directories, staged
// publication and restarted listings. Each completed run must then publish
// exactly the completed graph. Shrinking scenarios reach the directory first,
// through an incremental parent's retained edge, so the page that drops it
// must rebuild the reach.
func TestPagePropagationMatchesFullRebuildDB(t *testing.T) {
	type round struct {
		listing *scriptedListing
		skip    DirectorySkipper
		staged  bool
	}
	cases := map[string]struct {
		roots  []string
		rounds []round
	}{
		"aliases and cycles": {roots: []string{"a", "b", "z"}, rounds: []round{{listing: &scriptedListing{full: map[string][][]*storagev1.Entry{
			"a": {page(bookEntry("a1"), dirEntry("b"), dirEntry("c")), books("a2")},
			"b": {page(bookEntry("b1"), dirEntry("a"))},
			"c": {page(bookEntry("c1"), dirEntry("d")), books("c2")},
			"d": {books("d1", "a1")},
			"z": {page(bookEntry("z1"), dirEntry("a"))},
		}}}}},
		"root reaches a finished directory": {roots: []string{"m", "q"}, rounds: []round{{listing: &scriptedListing{full: map[string][][]*storagev1.Entry{
			"m": {page(dirEntry("n"))},
			"n": {books("n1"), page(dirEntry("o"), bookEntry("n2"))},
			"o": {books("o1"), books("o2")},
			"q": {page(dirEntry("n"), bookEntry("q1"))},
		}}}}},
		"incremental removal shrinks the reach": {roots: []string{"root"}, rounds: []round{
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("c1"), dirEntry("c2"), bookEntry("r1"))},
				"c1":   {books("k1")},
				"c2":   {page(bookEntry("k2"), dirEntry("c3"))},
				"c3":   {books("k3")},
			}}},
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"c4": {books("k4")},
			}, incremental: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("c4")), books("r2")},
			}, removed: map[string][]string{"root": {"c2"}}}},
		}},
		"skipped directory leaves the reach": {roots: []string{"root"}, rounds: []round{
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"root":   {page(dirEntry("keep"), dirEntry("skipme"))},
				"keep":   {books("k1")},
				"skipme": {books("s1")},
			}}},
			// The root's first page reaches skipme through its retained edge;
			// its second page applies the skip rule to it.
			{skip: func(e *storagev1.Entry) bool { return e.Id == "skipme" }, listing: &scriptedListing{tokens: true, incremental: map[string][][]*storagev1.Entry{
				"root": {page(bookEntry("r1")), page(dirEntry("skipme"))},
				"keep": {}, "skipme": {books("s2")},
			}}},
		}},
		"demoted directory leaves the reach": {roots: []string{"root"}, rounds: []round{
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("x"), bookEntry("r1"))},
				"x":    {books("x1")},
			}}},
			// The root's first page reaches x through its retained edge; its
			// second page lists x as a file.
			{listing: &scriptedListing{tokens: true, incremental: map[string][][]*storagev1.Entry{
				"root": {books("r2"), books("x")},
				"x":    {},
			}}},
		}},
		"promoted file becomes a directory": {roots: []string{"root"}, rounds: []round{
			{listing: &scriptedListing{full: map[string][][]*storagev1.Entry{
				"root": {page(bookEntry("x"), dirEntry("c"))},
				"c":    {books("c1")},
			}}},
			{listing: &scriptedListing{full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("x"), dirEntry("c"))},
				"c":    {books("c1")},
				"x":    {books("x1"), page(dirEntry("y"))},
				"y":    {books("y1")},
			}}},
		}},
		"alias promotion while the other parent is incremental": {roots: []string{"a", "q"}, rounds: []round{
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"a": {page(bookEntry("x"), bookEntry("a1"))},
				"q": {books("q1")},
			}}},
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"x": {books("x1")},
			}, incremental: map[string][][]*storagev1.Entry{
				"a": {},
				"q": {page(dirEntry("x"))},
			}}},
		}},
		"alias demotion while the other parent is incremental": {roots: []string{"a", "q"}, rounds: []round{
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"a": {page(dirEntry("x"), bookEntry("a1"))},
				"q": {books("q1")},
				"x": {books("x1")},
			}}},
			{listing: &scriptedListing{tokens: true, incremental: map[string][][]*storagev1.Entry{
				"a": {},
				"q": {page(bookEntry("x"))},
				"x": {},
			}}},
		}},
		"deep incremental tree gains a nested directory": {roots: []string{"root"}, rounds: []round{
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("a"), bookEntry("r1"))},
				"a":    {page(dirEntry("b"), bookEntry("a1"))},
				"b":    {page(dirEntry("c"), bookEntry("b1"))},
				"c":    {books("c1", "c2")},
			}}},
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"d": {books("d1"), page(dirEntry("e"))},
				"e": {books("e1")},
			}, incremental: map[string][][]*storagev1.Entry{
				"root": {}, "a": {}, "c": {},
				"b": {page(dirEntry("d"))},
			}}},
		}},
		"staged files publish on the completing page": {roots: []string{"m", "q"}, rounds: []round{
			{staged: true, listing: &scriptedListing{full: map[string][][]*storagev1.Entry{
				"m": {page(dirEntry("n"))},
				"n": {books("n1", "n2"), books("n3"), page(dirEntry("o"))},
				"o": {books("o1"), books("o2")},
				"q": {page(dirEntry("n"), bookEntry("q1"))},
			}}},
			{staged: true, listing: &scriptedListing{full: map[string][][]*storagev1.Entry{
				"m": {page(dirEntry("n"))},
				"n": {books("n1"), books("n4"), page(dirEntry("o"))},
				"o": {books("o1"), books("o3")},
				"q": {page(dirEntry("n"), bookEntry("q1"))},
			}}},
		}},
		"directory returns within its token window": {roots: []string{"root"}, rounds: []round{
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("dd"), bookEntry("r1"))},
				"dd":   {books("d1", "d2")},
			}}},
			{listing: &scriptedListing{tokens: true, incremental: map[string][][]*storagev1.Entry{
				"root": {},
			}, removed: map[string][]string{"root": {"dd"}}}},
			{listing: &scriptedListing{tokens: true, incremental: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("dd"))},
				"dd":   {page(), books("d3")},
			}}},
		}},
		"restarted listing rebuilds the reach": {roots: []string{"root"}, rounds: []round{
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("c1"), dirEntry("c2"))},
				"c1":   {books("k1")},
				"c2":   {books("k2")},
			}}},
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("c1"))},
			}, incremental: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("c3")), books("r2")},
				"c1":   {},
			}, reject: map[string]string{"root": "1"}}},
		}},
		"expired token on the first page restarts in full": {roots: []string{"root"}, rounds: []round{
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("c1"), dirEntry("c2"))},
				"c1":   {books("k1")},
				"c2":   {books("k2")},
			}}},
			{listing: &scriptedListing{tokens: true, full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("c2")), page(dirEntry("c3"))},
				"c3":   {books("k3")},
			}, incremental: map[string][][]*storagev1.Entry{
				"c1": {}, "c2": {},
			}, reject: map[string]string{"root": ""}}},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, _, l := scopedFixture(t, tc.roots...)
			for i, round := range tc.rounds {
				lease := oracleRun(t, r, l, round.listing, round.skip, round.staged)
				var state string
				if err := r.pool.QueryRow(t.Context(), `SELECT state FROM bloem_storage_scan_runs WHERE id=$1`, lease.RunID).Scan(&state); err != nil || state != "complete" {
					t.Fatalf("round %d state=%s err=%v", i, state, err)
				}
				checkCompletedLocation(t, r, l, lease, fmt.Sprintf("round %d", i))
			}
		})
	}
}

// A page writes memberships for its own entries only: listing a directory in
// full must not rewrite every membership the run already published.
func TestPagePropagationWritesAreBoundedByPageDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	execSQL(t, r.pool, `CREATE TABLE test_file_root_writes(n bigint NOT NULL)`)
	execSQL(t, r.pool, `INSERT INTO test_file_root_writes VALUES(0)`)
	execSQL(t, r.pool, `CREATE FUNCTION test_count_file_root_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE test_file_root_writes SET n=n+1; RETURN NULL; END $$`)
	execSQL(t, r.pool, `CREATE TRIGGER test_count_file_root_write AFTER INSERT OR UPDATE ON bloem_storage_file_roots FOR EACH ROW EXECUTE FUNCTION test_count_file_root_write()`)
	const pages, perPage = 30, 20
	listing := &scriptedListing{full: map[string][][]*storagev1.Entry{}}
	for p := range pages {
		ids := make([]string, 0, perPage)
		for i := range perPage {
			ids = append(ids, fmt.Sprintf("b%03d-%02d", p, i))
		}
		listing.full["root"] = append(listing.full["root"], books(ids...))
	}
	client := providerClient(t, listing.list)
	lease := scopedBegin(t, r, l)
	var calls int
	publish := scopedPublisher(t, l, &calls)
	writes := func() int {
		var n int
		if err := r.pool.QueryRow(t.Context(), `SELECT n FROM test_file_root_writes`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for p := range pages {
		cp, more, err := r.NextDirectory(t.Context(), lease)
		if err != nil || !more {
			t.Fatalf("page %d pending=%v err=%v", p, more, err)
		}
		listed, err := r.FetchPage(t.Context(), lease, cp, client)
		if err != nil {
			t.Fatal(err)
		}
		before := writes()
		if err = r.ApplyPage(t.Context(), lease, cp, listed, publish); err != nil {
			t.Fatal(err)
		}
		if delta := writes() - before; delta > perPage {
			t.Fatalf("page %d wrote %d memberships for %d entries", p, delta, perPage)
		}
	}
	var members int
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_file_roots WHERE location_id=$1 AND enabled`, l.ID).Scan(&members); err != nil || members != pages*perPage {
		t.Fatalf("published memberships=%d err=%v", members, err)
	}
	if err := r.Complete(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
}

// A committed page proves the lease as a renewal does, so page work that
// holds the library lock cannot let the lease lapse.
func TestPageCommitExtendsLeaseDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	lease, err := r.BeginLocationAuthorized(t.Context(), l, "worker", 30*time.Minute, func(context.Context, pgx.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()+interval '5 seconds' WHERE id=$1`, lease.RunID)
	cp, _, err := r.NextDirectory(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{Entries: books("book"), NextCursor: "1"}, nil); err != nil {
		t.Fatal(err)
	}
	var extended bool
	if err = r.pool.QueryRow(t.Context(), `SELECT lease_until>clock_timestamp()+interval '20 minutes' FROM bloem_storage_scan_runs WHERE id=$1`, lease.RunID).Scan(&extended); err != nil || !extended {
		t.Fatalf("page commit did not extend the lease: %v", err)
	}
	// A page that finds its lease already expired still fails and extends nothing.
	execSQL(t, r.pool, `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, lease.RunID)
	cp.Cursor = "1"
	if err = r.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{Entries: books("other"), Complete: true}, nil); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("expired lease accepted a page: %v", err)
	}
	if err = r.pool.QueryRow(t.Context(), `SELECT lease_until>clock_timestamp() FROM bloem_storage_scan_runs WHERE id=$1`, lease.RunID).Scan(&extended); err != nil || extended {
		t.Fatalf("expired lease revived: %v", err)
	}
}

// A renewal waits for the library lock a page transaction holds. The page's
// own commit renews the lease, so the waiting renewal can safely time out.
func TestRenewalWaitsBehindPageLibraryLockDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	lockLibrary := func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT 1 FROM media_folders WHERE id=$1 FOR UPDATE`, l.FolderID)
		return err
	}
	const ttl = 30 * time.Minute
	lease, err := r.BeginLocationAuthorized(t.Context(), l, "worker", ttl, lockLibrary)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()+interval '5 seconds' WHERE id=$1`, lease.RunID)
	cp, _, err := r.NextDirectory(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	holding, release := make(chan struct{}), make(chan struct{})
	applied := make(chan error, 1)
	go func() {
		applied <- r.ApplyPageAuthorized(t.Context(), lease, cp, &storagev1.ListResponse{Entries: books("book"), Complete: true}, lockLibrary, func(ctx context.Context, tx pgx.Tx, _ []*storagev1.Entry, _ []string) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	waiting, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	err = r.RenewAuthorized(waiting, lease, ttl, lockLibrary)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("renewal did not wait for the page's library lock: %v", err)
	}
	close(release)
	if err = <-applied; err != nil {
		t.Fatal(err)
	}
	var extended bool
	if err = r.pool.QueryRow(t.Context(), `SELECT lease_until>clock_timestamp()+interval '20 minutes' FROM bloem_storage_scan_runs WHERE id=$1`, lease.RunID).Scan(&extended); err != nil || !extended {
		t.Fatalf("page commit did not renew the lease: %v", err)
	}
	if err = r.RenewAuthorized(t.Context(), lease, ttl, lockLibrary); err != nil {
		t.Fatalf("renewal after the page: %v", err)
	}
}

// Renewal stops before completion, so completion renews the lease for its
// own work rather than racing what remains of the last renewal.
func TestCompletionRenewsLeaseForItsWorkDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	lease := scopedBegin(t, r, l)
	cp, _, err := r.NextDirectory(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{Entries: books("book"), Complete: true}, nil); err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()+interval '1 second' WHERE id=$1`, lease.RunID)
	slow := func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT pg_sleep(1.5)`)
		return err
	}
	if err = r.CompleteAuthorized(t.Context(), lease, func(context.Context, pgx.Tx) error { return nil }, slow); err != nil {
		t.Fatalf("completion outlasting the prior renewal failed: %v", err)
	}
}

// Reconciliation and the finalizer each get the lease's full duration, so a
// completion whose phases together outlast the lease commits. A phase that
// alone outlasts it still fails: completion never revives an expired lease.
func TestCompletionRenewsLeaseBetweenPhasesDB(t *testing.T) {
	const ttl, budget = 2 * time.Second, 3 * time.Second
	cases := map[string]struct {
		reconcile, finalize string
		wantStale           bool
	}{
		"phases together outlast the budget":      {reconcile: "2.2", finalize: "2.2"},
		"reconciliation outlasts the lease, only": {reconcile: "2.5", finalize: "0"},
		"reconciliation outlasts the budget":      {reconcile: "3.5", finalize: "0", wantStale: true},
		"finalizer outlasts the budget":           {reconcile: "0", finalize: "3.5", wantStale: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, _, l := scopedFixture(t, "root")
			r.completionBudget = budget
			lease, err := r.BeginLocationAuthorized(t.Context(), l, "worker", ttl, func(context.Context, pgx.Tx) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			cp, _, err := r.NextDirectory(t.Context(), lease)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.ApplyPage(t.Context(), lease, cp, &storagev1.ListResponse{Entries: books("book"), Complete: true}, nil); err != nil {
				t.Fatal(err)
			}
			// Reconciliation's membership statement stands in for a large location's.
			execSQL(t, r.pool, `CREATE FUNCTION test_slow_reconcile() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(`+tc.reconcile+`); RETURN NULL; END $$`)
			execSQL(t, r.pool, `CREATE TRIGGER test_slow_reconcile AFTER INSERT ON bloem_storage_file_roots FOR EACH STATEMENT EXECUTE FUNCTION test_slow_reconcile()`)
			slow := func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `SELECT pg_sleep(`+tc.finalize+`)`)
				return err
			}
			err = r.CompleteAuthorized(t.Context(), lease, func(context.Context, pgx.Tx) error { return nil }, slow)
			if tc.wantStale {
				if !errors.Is(err, ErrStaleLease) {
					t.Fatalf("completion phase outlasting the budget: %v, want ErrStaleLease", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("completion whose phases each fit the budget: %v", err)
			}
		})
	}
}

// Completing an unchanged location rewrites no membership or entry.
func TestUnchangedCompletionRewritesNothingDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	execSQL(t, r.pool, `CREATE TABLE test_completion_writes(n bigint NOT NULL)`)
	execSQL(t, r.pool, `INSERT INTO test_completion_writes VALUES(0)`)
	execSQL(t, r.pool, `CREATE FUNCTION test_count_completion_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE test_completion_writes SET n=n+1; RETURN NULL; END $$`)
	execSQL(t, r.pool, `CREATE TRIGGER test_count_completion_write AFTER INSERT OR UPDATE ON bloem_storage_file_roots FOR EACH ROW EXECUTE FUNCTION test_count_completion_write()`)
	execSQL(t, r.pool, `CREATE TRIGGER test_count_completion_write AFTER UPDATE ON bloem_storage_location_entries FOR EACH ROW EXECUTE FUNCTION test_count_completion_write()`)
	listing := &scriptedListing{full: map[string][][]*storagev1.Entry{
		"root": {page(dirEntry("sub"), bookEntry("r1")), books("r2", "r3")},
		"sub":  {books("s1", "s2")},
	}}
	client := providerClient(t, listing.list)
	var calls int
	publish := scopedPublisher(t, l, &calls)
	writes := func() int {
		var n int
		if err := r.pool.QueryRow(t.Context(), `SELECT n FROM test_completion_writes`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for round := range 2 {
		lease := scopedBegin(t, r, l)
		for {
			cp, more, err := r.NextDirectory(t.Context(), lease)
			if err != nil {
				t.Fatal(err)
			}
			if !more {
				break
			}
			listed, err := r.FetchPage(t.Context(), lease, cp, client)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.ApplyPage(t.Context(), lease, cp, listed, publish); err != nil {
				t.Fatal(err)
			}
		}
		before := writes()
		if err := r.Complete(t.Context(), lease); err != nil {
			t.Fatal(err)
		}
		if delta := writes() - before; round == 1 && delta != 0 {
			t.Fatalf("unchanged completion rewrote %d rows", delta)
		}
	}
	var members int
	if err := r.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_file_roots WHERE location_id=$1 AND enabled`, l.ID).Scan(&members); err != nil || members != 5 {
		t.Fatalf("memberships=%d err=%v", members, err)
	}
}

// A replayed page proves the lease as its first application did.
func TestReplayedPageRenewsLeaseDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	lease := scopedBegin(t, r, l)
	cp, _, err := r.NextDirectory(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	listed := &storagev1.ListResponse{Entries: books("book"), NextCursor: "1"}
	if err = r.ApplyPage(t.Context(), lease, cp, listed, nil); err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()+interval '5 seconds' WHERE id=$1`, lease.RunID)
	if err = r.ApplyPage(t.Context(), lease, cp, listed, nil); err != nil {
		t.Fatalf("replay: %v", err)
	}
	var extended bool
	if err = r.pool.QueryRow(t.Context(), `SELECT lease_until>clock_timestamp()+interval '30 seconds' FROM bloem_storage_scan_runs WHERE id=$1`, lease.RunID).Scan(&extended); err != nil || !extended {
		t.Fatalf("replayed page did not renew the lease: %v", err)
	}
}
