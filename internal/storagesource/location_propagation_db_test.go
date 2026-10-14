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
// full rebuild. It returns the completed lease.
func oracleRun(t *testing.T, r *Repository, l Location, listing *scriptedListing, skip DirectorySkipper) Lease {
	t.Helper()
	client := providerClient(t, listing.list)
	lease := scopedBegin(t, r, l)
	oracle := newFullRebuildOracle(t, r, lease)
	var calls int
	publish := scopedPublisher(t, l, &calls)
	for steps := 0; steps < 100; steps++ {
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
		if err = r.applyPage(t.Context(), lease, cp, page, skip, nil, publish); err != nil {
			t.Fatal(err)
		}
		oracle.check(t, fmt.Sprintf("page %d of %s@%q", steps, cp.DirectoryID, cp.Cursor))
	}
	t.Fatal("directory graph did not terminate")
	return lease
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
// listings, skipped directories, demoted directories and restarted listings.
func TestPagePropagationMatchesFullRebuildDB(t *testing.T) {
	type round struct {
		listing *scriptedListing
		skip    DirectorySkipper
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
			{listing: &scriptedListing{full: map[string][][]*storagev1.Entry{
				"root":   {page(dirEntry("keep"), dirEntry("skipme"))},
				"keep":   {books("k1")},
				"skipme": {books("s1")},
			}}},
			{listing: &scriptedListing{full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("skipme")), page(dirEntry("keep"))},
				"keep": {books("k1")},
			}}, skip: func(e *storagev1.Entry) bool { return e.Id == "skipme" }},
		}},
		"demoted directory leaves the reach": {roots: []string{"root"}, rounds: []round{
			{listing: &scriptedListing{full: map[string][][]*storagev1.Entry{
				"root": {page(dirEntry("x"), bookEntry("r1"))},
				"x":    {books("x1")},
			}}},
			{listing: &scriptedListing{full: map[string][][]*storagev1.Entry{
				"root": {books("r1"), books("x")},
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
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, _, l := scopedFixture(t, tc.roots...)
			for i, round := range tc.rounds {
				lease := oracleRun(t, r, l, round.listing, round.skip)
				var state string
				if err := r.pool.QueryRow(t.Context(), `SELECT state FROM bloem_storage_scan_runs WHERE id=$1`, lease.RunID).Scan(&state); err != nil || state != "complete" {
					t.Fatalf("round %d state=%s err=%v", i, state, err)
				}
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
