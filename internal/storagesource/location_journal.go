package storagesource

import (
	"context"
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// Directory evidence is separate from source staging: aliases may share an
// entry identity, but a provider cannot repeat one ID across a directory's pages.
// shrunk reports that the page removed an edge to a directory or turned a
// directory into a file, either of which can take directories out of the
// run's reach.
func applyLocationEntries(ctx context.Context, tx pgx.Tx, lease Lease, cp Checkpoint, page *storagev1.ListResponse, skip DirectorySkipper) (files []*storagev1.Entry, shrunk bool, err error) {
	var mode string
	if err := tx.QueryRow(ctx, `SELECT listing_mode FROM bloem_storage_scan_directories WHERE run_id=$1 AND directory_id=$2`, lease.RunID, cp.DirectoryID).Scan(&mode); err != nil {
		return nil, false, err
	}
	if mode == "pending" {
		mode = "full"
		if _, err := tx.Exec(ctx, `UPDATE bloem_storage_scan_directories SET listing_mode='full' WHERE run_id=$1 AND directory_id=$2`, lease.RunID, cp.DirectoryID); err != nil {
			return nil, false, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_location_directories(location_id,directory_id,configuration_revision,selection_revision) VALUES($1,$2,$3,$4) ON CONFLICT(location_id,directory_id) DO UPDATE SET configuration_revision=EXCLUDED.configuration_revision,selection_revision=EXCLUDED.selection_revision`, lease.LocationID, cp.DirectoryID, lease.ConfigurationRevision, lease.SelectionRevision); err != nil {
		return nil, false, err
	}
	var listedFiles []string
	for _, e := range page.Entries {
		if e.Kind != storagev1.EntryKind_ENTRY_KIND_DIRECTORY {
			listedFiles = append(listedFiles, e.Id)
		}
	}
	if len(listedFiles) > 0 {
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_storage_location_entries WHERE location_id=$1 AND entry_id=ANY($2::text[]) AND kind=2)`, lease.LocationID, listedFiles).Scan(&shrunk); err != nil {
			return nil, false, err
		}
	}

	ids := make([]string, 0, len(page.Entries)+len(page.RemovedEntryIds))
	removed := make([]bool, 0, cap(ids))
	for _, e := range page.Entries {
		ids = append(ids, e.Id)
		removed = append(removed, false)
	}
	for _, id := range page.RemovedEntryIds {
		ids = append(ids, id)
		removed = append(removed, true)
	}
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_scan_directory_entries(run_id,directory_id,entry_id,removed) SELECT $1,$2,x.id,x.removed FROM unnest($3::text[],$4::bool[]) x(id,removed)`, lease.RunID, cp.DirectoryID, ids, removed); err != nil {
			if isUniqueViolation(err) {
				return nil, false, ErrReferenceConflict
			}
			return nil, false, err
		}
	}
	files, skipped, err := recordLocationEntriesTx(ctx, tx, lease, page.Entries, skip)
	if err != nil {
		return nil, false, err
	}
	edges := make([]string, 0, len(page.Entries))
	for _, e := range page.Entries {
		if !skipped[e.Id] {
			edges = append(edges, e.Id)
		}
	}
	if len(skipped) > 0 {
		tag, err := tx.Exec(ctx, `DELETE FROM bloem_storage_location_edges WHERE location_id=$1 AND parent_directory_id=$2 AND entry_id=ANY($3::text[])`, lease.LocationID, cp.DirectoryID, slices.Collect(maps.Keys(skipped)))
		if err != nil {
			return nil, false, err
		}
		shrunk = shrunk || tag.RowsAffected() > 0
	}
	if len(edges) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_location_edges(location_id,parent_directory_id,entry_id) SELECT $1,$2,unnest($3::text[])
 ON CONFLICT(location_id,parent_directory_id,entry_id) DO UPDATE SET absence_suspected_run=NULL`, lease.LocationID, cp.DirectoryID, edges); err != nil {
			return nil, false, err
		}
	}
	if len(page.RemovedEntryIds) > 0 {
		var removedDirectory bool
		if err := tx.QueryRow(ctx, `WITH gone AS (DELETE FROM bloem_storage_location_edges WHERE location_id=$1 AND parent_directory_id=$2 AND entry_id=ANY($3::text[]) RETURNING entry_id)
 SELECT EXISTS(SELECT 1 FROM gone JOIN bloem_storage_location_entries x ON x.location_id=$1 AND x.entry_id=gone.entry_id AND x.kind=2)`, lease.LocationID, cp.DirectoryID, page.RemovedEntryIds).Scan(&removedDirectory); err != nil {
			return nil, false, err
		}
		shrunk = shrunk || removedDirectory
	}
	return files, shrunk, nil
}

// recordLocationEntriesTx records a page's entries in a few statements, not
// several per entry. An entry this run already recorded must be listed the
// same again; the others are written and their files returned, in page order.
// skipped holds the page's directories the library does not descend into.
func recordLocationEntriesTx(ctx context.Context, tx pgx.Tx, lease Lease, entries []*storagev1.Entry, skip DirectorySkipper) (files []*storagev1.Entry, skipped map[string]bool, err error) {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.Id
	}
	type seen struct {
		kind           int32
		size, modified int64
		revision       string
	}
	recorded := map[string]seen{}
	rows, err := tx.Query(ctx, `SELECT entry_id,kind,size,modified_unix_nano,revision FROM bloem_storage_location_entries WHERE location_id=$1 AND entry_id=ANY($2::text[]) AND last_seen_run=$3`, lease.LocationID, ids, lease.RunID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		var old seen
		if err := rows.Scan(&id, &old.kind, &old.size, &old.modified, &old.revision); err != nil {
			rows.Close()
			return nil, nil, err
		}
		recorded[id] = old
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var fresh struct {
		ids, names, paths, revisions []string
		kinds                        []int32
		sizes, modified              []int64
	}
	skipped = map[string]bool{}
	for _, e := range entries {
		if old, ok := recorded[e.Id]; ok {
			if old.kind != int32(e.Kind) || old.size != e.Size || old.modified != e.ModifiedUnixNano || old.revision != e.Revision {
				return nil, nil, ErrReferenceConflict
			}
		} else {
			fresh.ids, fresh.names, fresh.paths, fresh.revisions = append(fresh.ids, e.Id), append(fresh.names, e.Name), append(fresh.paths, e.LogicalPath), append(fresh.revisions, e.Revision)
			fresh.kinds, fresh.sizes, fresh.modified = append(fresh.kinds, int32(e.Kind)), append(fresh.sizes, e.Size), append(fresh.modified, e.ModifiedUnixNano)
			if e.Kind == storagev1.EntryKind_ENTRY_KIND_FILE {
				files = append(files, e)
			}
		}
		if e.Kind == storagev1.EntryKind_ENTRY_KIND_DIRECTORY && skip != nil && skip(e) {
			skipped[e.Id] = true
		}
	}
	if len(fresh.ids) == 0 {
		return files, skipped, nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_location_entries(location_id,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,selection_revision,last_seen_run)
 SELECT $1,n.id,n.name,n.path,n.kind,n.size,n.modified,n.revision,$9,$10,$11 FROM unnest($2::text[],$3::text[],$4::text[],$5::int[],$6::bigint[],$7::bigint[],$8::text[]) n(id,name,path,kind,size,modified,revision)
 ON CONFLICT(location_id,entry_id) DO UPDATE SET name=EXCLUDED.name,logical_path=EXCLUDED.logical_path,kind=EXCLUDED.kind,size=EXCLUDED.size,modified_unix_nano=EXCLUDED.modified_unix_nano,revision=EXCLUDED.revision,configuration_revision=EXCLUDED.configuration_revision,selection_revision=EXCLUDED.selection_revision,last_seen_run=EXCLUDED.last_seen_run,absence_confirmed_at=NULL`,
		lease.LocationID, fresh.ids, fresh.names, fresh.paths, fresh.kinds, fresh.sizes, fresh.modified, fresh.revisions, lease.ConfigurationRevision, lease.SelectionRevision, lease.RunID); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run)
 SELECT $1,n.id,n.name,n.path,n.kind,n.size,n.modified,n.revision,$9,$10 FROM unnest($2::text[],$3::text[],$4::text[],$5::int[],$6::bigint[],$7::bigint[],$8::text[]) n(id,name,path,kind,size,modified,revision)
 ON CONFLICT(source_key,entry_id) DO UPDATE SET name=EXCLUDED.name,logical_path=EXCLUDED.logical_path,kind=EXCLUDED.kind,size=EXCLUDED.size,modified_unix_nano=EXCLUDED.modified_unix_nano,revision=EXCLUDED.revision,configuration_revision=EXCLUDED.configuration_revision,last_seen_run=EXCLUDED.last_seen_run,absence_confirmed_at=NULL`,
		lease.SourceKey, fresh.ids, fresh.names, fresh.paths, fresh.kinds, fresh.sizes, fresh.modified, fresh.revisions, lease.ConfigurationRevision, lease.RunID); err != nil {
		return nil, nil, err
	}
	return files, skipped, nil
}

// UNION (not UNION ALL) terminates cycles and carries each root across aliases,
// including aliases discovered after the target directory finished. Edges from
// a pending full traversal cannot prove descent until its first page commits.
// A full listing's edge proves descent only once this run has observed it.
const locationReachSQL = `WITH RECURSIVE reach(directory_id,root_id) AS (
 SELECT directory_id,id FROM bloem_storage_selected_roots WHERE location_id=$2 AND enabled
 UNION
 SELECT e.entry_id,r.root_id FROM reach r
 JOIN bloem_storage_scan_directories d ON d.run_id=$1 AND d.directory_id=r.directory_id AND d.listing_mode<>'pending'
 JOIN bloem_storage_location_edges e ON e.location_id=$2 AND e.parent_directory_id=r.directory_id AND e.absence_suspected_run IS NULL
 AND (d.listing_mode<>'full' OR EXISTS(SELECT 1 FROM bloem_storage_scan_directory_entries observed WHERE observed.run_id=$1 AND observed.directory_id=r.directory_id AND observed.entry_id=e.entry_id AND NOT observed.removed))
 JOIN bloem_storage_location_entries x ON x.location_id=e.location_id AND x.entry_id=e.entry_id AND x.kind=2
 WHERE x.configuration_revision=$3
) `

// pageReach is what one committed page can change in its run's reach.
type pageReach struct {
	directoryID string
	// entries are the page's listed entry IDs. Their edges, from every
	// parent, may begin to count: a new edge, a cleared suspicion, a first
	// observation, or a changed entry kind, revision or reference.
	entries []string
	// opened marks the directory's first page. Its retained edges begin to
	// count, and its files under its roots become publishable.
	opened bool
	// closed marks the directory's completing page, which may publish files
	// staged from its earlier pages.
	closed bool
	// shrunk marks a page that removed or demoted a directory edge.
	shrunk bool
}

// propagatePageRootsTx extends the run's reach and published root memberships
// with what one page changed, so a page costs its own entries plus the
// directories it newly reached rather than the location's whole graph. A page
// that may have shrunk the reach rebuilds it. Only positive evidence is
// published before completion; reconcileLocationTx rebuilds every membership
// from the completed graph.
func propagatePageRootsTx(ctx context.Context, tx pgx.Tx, lease Lease, page pageReach) error {
	if lease.CarryUnchanged {
		if err := carryUnchangedTx(ctx, tx, lease, page); err != nil {
			return err
		}
	}
	if page.shrunk {
		if err := rebuildLocationReachTx(ctx, tx, lease); err != nil {
			return err
		}
		return publishReachedMembershipsTx(ctx, tx, lease)
	}
	reached, err := extendLocationReachTx(ctx, tx, lease, edgesIntoEntries, page.entries)
	if err != nil {
		return err
	}
	if page.opened {
		more, err := extendLocationReachTx(ctx, tx, lease, edgesOfDirectory, page.directoryID)
		if err != nil {
			return err
		}
		reached = append(reached, more...)
	}
	dirs, roots := make([]string, 0, len(reached)), make([]uuid.UUID, 0, len(reached))
	seen := make(map[reachedPair]bool, len(reached))
	for _, pair := range reached {
		if !seen[pair] {
			seen[pair] = true
			dirs, roots = append(dirs, pair.directoryID), append(roots, pair.rootID)
		}
	}
	if len(dirs) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_scan_directories(run_id,directory_id) SELECT DISTINCT $1::uuid,d FROM unnest($2::text[]) d ON CONFLICT DO NOTHING`, lease.RunID, dirs); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_scan_directory_roots(run_id,directory_id,location_id,root_id) SELECT $1::uuid,n.d,$2::uuid,n.r FROM unnest($3::text[],$4::uuid[]) n(d,r) ON CONFLICT DO NOTHING`, lease.RunID, lease.LocationID, dirs, roots); err != nil {
			return err
		}
	}
	args := []any{lease.RunID, lease.LocationID, lease.ConfigurationRevision}
	if err := pageMembershipTx(ctx, tx, edgesIntoEntries, append(args, page.entries)...); err != nil {
		return err
	}
	if page.opened || page.closed {
		if err := pageMembershipTx(ctx, tx, edgesIntoChildren, append(args, page.directoryID)...); err != nil {
			return err
		}
	}
	if len(dirs) > 0 {
		if err := pageMembershipTx(ctx, tx, edgesOfReached, append(args, dirs, roots)...); err != nil {
			return err
		}
	}
	return nil
}

// pageMembershipTx publishes the memberships of edges a page bounds, under a
// generic plan. Given the page's own arguments, the planner still anti-joins
// every membership and file reference of the location, a cost that grows with
// the library (49 ms per book at 6,000 tracks); the generic plan starts from
// the page's edges (0.2 ms).
func pageMembershipTx(ctx context.Context, tx pgx.Tx, source edgeSource, args ...any) error {
	if _, err := tx.Exec(ctx, `SET LOCAL plan_cache_mode = force_generic_plan`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, membershipSQL(source), args...); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SET LOCAL plan_cache_mode TO DEFAULT`)
	return err
}

// carryUnchangedTx applies an incremental listing's evidence about its child
// directories in a source whose change feed covers their contents. A child it
// lists is listed in full; on its first page every other retained child
// directory is carried: complete for this run, its retained entries kept and
// counted like an incremental directory's, without a listing. A full listing
// carries nothing, so an expired change token reconciles every child.
func carryUnchangedTx(ctx context.Context, tx pgx.Tx, lease Lease, page pageReach) error {
	var mode string
	if err := tx.QueryRow(ctx, `SELECT listing_mode FROM bloem_storage_scan_directories WHERE run_id=$1 AND directory_id=$2`, lease.RunID, page.directoryID).Scan(&mode); err != nil {
		return err
	}
	if mode != "incremental" {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE bloem_storage_scan_directories SET listing_mode='pending',complete=false WHERE run_id=$1 AND directory_id=ANY($2::text[]) AND listing_mode='carried'`, lease.RunID, page.entries); err != nil {
		return err
	}
	if !page.opened {
		return nil
	}
	// A book whose track an earlier scan could not read is listed again.
	_, err := tx.Exec(ctx, `WITH retry AS (SELECT DISTINCT t.parent_directory_id FROM bloem_storage_audio_probes p
 JOIN bloem_storage_location_edges t ON t.location_id=p.location_id AND t.entry_id=p.entry_id WHERE p.location_id=$2 AND p.status='unavailable')
 INSERT INTO bloem_storage_scan_directories(run_id,directory_id,listing_mode,complete)
 SELECT DISTINCT $1::uuid,e.entry_id,CASE WHEN r.parent_directory_id IS NULL THEN 'carried' ELSE 'pending' END,r.parent_directory_id IS NULL FROM bloem_storage_location_edges e
 JOIN bloem_storage_location_entries x ON x.location_id=e.location_id AND x.entry_id=e.entry_id AND x.kind=2 AND x.configuration_revision=$3
 LEFT JOIN retry r ON r.parent_directory_id=e.entry_id
 WHERE e.location_id=$2 AND e.parent_directory_id=$4 AND e.absence_suspected_run IS NULL AND NOT e.entry_id=ANY($5::text[])
 ON CONFLICT DO NOTHING`, lease.RunID, lease.LocationID, lease.ConfigurationRevision, page.directoryID, page.entries)
	return err
}

type reachedPair struct {
	directoryID string
	rootID      uuid.UUID
}

// edgeSource names the location edges e a statement visits: the FROM items
// that yield e and the condition restricting them. Page-sized sources are
// bounded by their argument arrays, so their plans stay bounded by the page
// even when table statistics lag a growing location.
// A source may first materialize the rows it starts from (with).
type edgeSource struct{ with, from, where string }

var (
	// Edges, from every parent, into the page's listed entries ($4). As an
	// array condition the planner sees how many entries the page lists; as a
	// join from unnest it could walk every edge of the location instead.
	edgesIntoEntries = edgeSource{``, `bloem_storage_location_edges e`, `e.entry_id=ANY($4::text[])`}
	// The listed directory's ($4) own edges.
	edgesOfDirectory = edgeSource{``, `bloem_storage_location_edges e`, `e.parent_directory_id=$4`}
	// Edges, from every parent, into the listed directory's ($4) children,
	// starting from those children.
	edgesIntoChildren = edgeSource{`WITH c AS MATERIALIZED (SELECT location_id,entry_id FROM bloem_storage_location_edges WHERE location_id=$2 AND parent_directory_id=$4) `,
		`c JOIN bloem_storage_location_edges e ON e.location_id=c.location_id AND e.entry_id=c.entry_id`, `true`}
	// Edges of newly reached directories ($4), under their new roots ($5).
	edgesOfReached = edgeSource{``, `unnest($4::text[],$5::uuid[]) n(directory_id,root_id) JOIN bloem_storage_location_edges e ON e.location_id=$2 AND e.parent_directory_id=n.directory_id`, `r.root_id=n.root_id`}
	// Every edge of the location.
	edgesOfLocation = edgeSource{``, `bloem_storage_location_edges e`, `true`}
)

// countedEdgeSQL holds when edge e, from a directory d this run has begun to
// list, proves descent into directory x under the current configuration.
const countedEdgeSQL = `d.listing_mode<>'pending' AND e.absence_suspected_run IS NULL
 AND (d.listing_mode<>'full' OR EXISTS(SELECT 1 FROM bloem_storage_scan_directory_entries observed WHERE observed.run_id=$1 AND observed.directory_id=e.parent_directory_id AND observed.entry_id=e.entry_id AND NOT observed.removed))
 AND x.kind=2 AND x.configuration_revision=$3`

// extendLocationReachTx returns the (directory, root) pairs newly reached
// through the seeded edges and, transitively, through every directory they
// newly reach. A pair already in the run's reach was expanded when it was
// reached, so the walk stops there.
//
// The walk takes one statement per level, each started from the previous
// level's pairs, so every plan stays bounded by what the page reached. As
// one recursive statement, the planner could not see how little a page
// reaches and hashed every edge of the location for each page, even for a
// page of files that reaches nothing.
func extendLocationReachTx(ctx context.Context, tx pgx.Tx, lease Lease, seed edgeSource, arg any) ([]reachedPair, error) {
	frontier, err := reachedPairsTx(ctx, tx, `SELECT DISTINCT e.entry_id,r.root_id FROM `+seed.from+`
 JOIN bloem_storage_scan_directory_roots r ON r.run_id=$1 AND r.directory_id=e.parent_directory_id
 JOIN bloem_storage_scan_directories d ON d.run_id=$1 AND d.directory_id=e.parent_directory_id
 JOIN bloem_storage_location_entries x ON x.location_id=e.location_id AND x.entry_id=e.entry_id
 WHERE e.location_id=$2 AND `+seed.where+` AND `+countedEdgeSQL+`
 AND NOT EXISTS(SELECT 1 FROM bloem_storage_scan_directory_roots k WHERE k.run_id=$1 AND k.directory_id=e.entry_id AND k.root_id=r.root_id)`,
		lease.RunID, lease.LocationID, lease.ConfigurationRevision, arg)
	var reached []reachedPair
	seen := map[reachedPair]bool{}
	for err == nil && len(frontier) > 0 {
		dirs, roots := make([]string, 0, len(frontier)), make([]uuid.UUID, 0, len(frontier))
		for _, pair := range frontier {
			if !seen[pair] {
				seen[pair] = true
				reached = append(reached, pair)
				dirs, roots = append(dirs, pair.directoryID), append(roots, pair.rootID)
			}
		}
		if len(dirs) == 0 {
			break
		}
		frontier, err = reachedPairsTx(ctx, tx, `SELECT DISTINCT e.entry_id,n.root_id FROM unnest($4::text[],$5::uuid[]) n(directory_id,root_id)
 JOIN bloem_storage_scan_directories d ON d.run_id=$1 AND d.directory_id=n.directory_id
 JOIN bloem_storage_location_edges e ON e.location_id=$2 AND e.parent_directory_id=n.directory_id
 JOIN bloem_storage_location_entries x ON x.location_id=e.location_id AND x.entry_id=e.entry_id
 WHERE `+countedEdgeSQL+`
 AND NOT EXISTS(SELECT 1 FROM bloem_storage_scan_directory_roots k WHERE k.run_id=$1 AND k.directory_id=e.entry_id AND k.root_id=n.root_id)`,
			lease.RunID, lease.LocationID, lease.ConfigurationRevision, dirs, roots)
	}
	return reached, err
}

func reachedPairsTx(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]reachedPair, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (reachedPair, error) {
		var pair reachedPair
		err := row.Scan(&pair.directoryID, &pair.rootID)
		return pair, err
	})
}

// membershipSQL publishes, for the source's edges, each file under every root
// of its listed parent. Memberships already enabled are left unwritten, so
// re-reaching a file costs a read, not a new row version.
func membershipSQL(source edgeSource) string {
	return source.with + `INSERT INTO bloem_storage_file_roots(location_id,media_file_id,root_id)
 SELECT DISTINCT $2::uuid,f.media_file_id,r.root_id FROM ` + source.from + `
 JOIN bloem_storage_scan_directory_roots r ON r.run_id=$1 AND r.directory_id=e.parent_directory_id
 JOIN bloem_storage_scan_directories d ON d.run_id=$1 AND d.directory_id=e.parent_directory_id AND d.listing_mode<>'pending'
 JOIN bloem_storage_file_refs f ON f.location_id=e.location_id AND f.entry_id=e.entry_id
 JOIN bloem_storage_location_entries x ON x.location_id=f.location_id AND x.entry_id=f.entry_id AND x.kind=1 AND x.revision=f.revision AND x.configuration_revision=f.configuration_revision
 WHERE e.location_id=$2 AND f.configuration_revision=$3 AND ` + source.where + `
 AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_roots m WHERE m.location_id=$2 AND m.media_file_id=f.media_file_id AND m.root_id=r.root_id AND m.enabled)
 ON CONFLICT(location_id,media_file_id,root_id) DO UPDATE SET enabled=true`
}

// rebuildLocationReachTx replaces the run's reach with the closure of its
// current graph and queues every directory it reaches.
func rebuildLocationReachTx(ctx context.Context, tx pgx.Tx, lease Lease) error {
	args := []any{lease.RunID, lease.LocationID, lease.ConfigurationRevision}
	if _, err := tx.Exec(ctx, locationReachSQL+`INSERT INTO bloem_storage_scan_directories(run_id,directory_id) SELECT DISTINCT $1,directory_id FROM reach ON CONFLICT DO NOTHING`, args...); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM bloem_storage_scan_directory_roots WHERE run_id=$1`, lease.RunID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, locationReachSQL+`INSERT INTO bloem_storage_scan_directory_roots(run_id,directory_id,location_id,root_id) SELECT $1,directory_id,$2,root_id FROM reach`, args...)
	return err
}

// publishReachedMembershipsTx publishes every file under the run's reach.
func publishReachedMembershipsTx(ctx context.Context, tx pgx.Tx, lease Lease) error {
	_, err := tx.Exec(ctx, membershipSQL(edgesOfLocation), lease.RunID, lease.LocationID, lease.ConfigurationRevision)
	return err
}

// Completion reconciles only this location's retained directory edges. A full
// listing can defer present entries, so its omissions need a later completed
// listing to confirm. Positive observations clear suspicion in the page transaction.
const completedLocationReachSQL = `WITH RECURSIVE reach(directory_id,root_id) AS (
 SELECT directory_id,id FROM bloem_storage_selected_roots WHERE location_id=$1 AND enabled
 UNION
 SELECT e.entry_id,r.root_id FROM reach r
 JOIN bloem_storage_location_edges e ON e.location_id=$1 AND e.parent_directory_id=r.directory_id
 JOIN bloem_storage_location_entries x ON x.location_id=e.location_id AND x.entry_id=e.entry_id AND x.kind=2
 WHERE x.configuration_revision=$2
) `

// completedMembershipSQL selects each file under every root the completed
// graph reaches.
const completedMembershipSQL = `SELECT DISTINCT f.media_file_id,r.root_id FROM reach r
 JOIN bloem_storage_location_edges e ON e.location_id=$1 AND e.parent_directory_id=r.directory_id
 JOIN bloem_storage_file_refs f ON f.location_id=e.location_id AND f.entry_id=e.entry_id
 JOIN bloem_storage_location_entries x ON x.location_id=f.location_id AND x.entry_id=f.entry_id AND x.kind=1 AND x.revision=f.revision AND x.configuration_revision=f.configuration_revision
 WHERE f.configuration_revision=$2`

func reconcileLocationTx(ctx context.Context, tx pgx.Tx, lease Lease) (int, error) {
	// Negative evidence from an older configuration or selection cannot confirm
	// absence under this retained authority. Expired run history is conservative.
	if _, err := tx.Exec(ctx, `UPDATE bloem_storage_location_edges e SET absence_suspected_run=NULL
 WHERE e.location_id=$1 AND e.absence_suspected_run IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM bloem_storage_scan_runs r WHERE r.id=e.absence_suspected_run AND r.location_id=$1
 AND r.configuration_revision=$2 AND r.selection_revision=$3 AND r.state='complete')`, lease.LocationID, lease.ConfigurationRevision, lease.SelectionRevision); err != nil {
		return 0, err
	}
	// An empty full listing is suspect and confirms no prior omission.
	if _, err := tx.Exec(ctx, `DELETE FROM bloem_storage_location_edges e USING bloem_storage_scan_directories d
 WHERE e.location_id=$1 AND d.run_id=$2 AND d.directory_id=e.parent_directory_id AND d.complete
 AND (d.listing_mode='incremental' OR (d.listing_mode='full' AND d.processed_count>0))
 AND e.absence_suspected_run IS NOT NULL AND e.absence_suspected_run<>$2
 AND NOT EXISTS(SELECT 1 FROM bloem_storage_scan_directory_entries x WHERE x.run_id=$2 AND x.directory_id=e.parent_directory_id AND x.entry_id=e.entry_id AND NOT x.removed)`, lease.LocationID, lease.RunID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE bloem_storage_location_edges e SET absence_suspected_run=$2
 FROM bloem_storage_scan_directories d WHERE e.location_id=$1 AND d.run_id=$2 AND d.directory_id=e.parent_directory_id
 AND d.complete AND d.listing_mode='full' AND d.processed_count>0 AND e.absence_suspected_run IS NULL
 AND NOT EXISTS(SELECT 1 FROM bloem_storage_scan_directory_entries x WHERE x.run_id=$2 AND x.directory_id=e.parent_directory_id AND x.entry_id=e.entry_id AND NOT x.removed)`, lease.LocationID, lease.RunID); err != nil {
		return 0, err
	}
	// Disable the memberships the completed graph no longer reaches, then
	// publish the reachable ones that are missing or disabled. The result is
	// the reachable set exactly, as disabling every membership and enabling
	// the reachable ones would leave it, without rewriting unchanged rows.
	// One statement computes the reachable set once for both writes. They
	// touch disjoint rows, enabled unreachable ones and disabled or missing
	// reachable ones, so neither sees the other's change.
	args := []any{lease.LocationID, lease.ConfigurationRevision}
	if _, err := tx.Exec(ctx, completedLocationReachSQL+`, reachable AS MATERIALIZED (`+completedMembershipSQL+`),
 disabled AS (UPDATE bloem_storage_file_roots m SET enabled=false WHERE m.location_id=$1 AND m.enabled
 AND NOT EXISTS(SELECT 1 FROM reachable v WHERE v.media_file_id=m.media_file_id AND v.root_id=m.root_id))
 INSERT INTO bloem_storage_file_roots(location_id,media_file_id,root_id)
 SELECT $1::uuid,v.media_file_id,v.root_id FROM reachable v
 WHERE NOT EXISTS(SELECT 1 FROM bloem_storage_file_roots m WHERE m.location_id=$1 AND m.media_file_id=v.media_file_id AND m.root_id=v.root_id AND m.enabled)
 ON CONFLICT(location_id,media_file_id,root_id) DO UPDATE SET enabled=true`, args...); err != nil {
		return 0, err
	}
	// Entries still reached keep a NULL confirmation; every absent entry is
	// stamped with this completion. Present entries already NULL are skipped.
	const reached = `EXISTS(SELECT 1 FROM bloem_storage_location_edges edge JOIN reach r ON r.directory_id=edge.parent_directory_id
 WHERE edge.location_id=e.location_id AND edge.entry_id=e.entry_id)`
	if _, err := tx.Exec(ctx, completedLocationReachSQL+`UPDATE bloem_storage_location_entries e SET absence_confirmed_at=CASE WHEN `+reached+` THEN NULL ELSE clock_timestamp() END
 WHERE e.location_id=$1 AND (e.absence_confirmed_at IS NOT NULL OR NOT `+reached+`)`, args...); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `UPDATE media_files f SET missing_since=COALESCE(f.missing_since,clock_timestamp()) FROM bloem_storage_file_refs x
 WHERE x.location_id=$1 AND x.media_file_id=f.id AND f.missing_since IS NULL AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_roots m JOIN bloem_storage_selected_roots r ON r.location_id=m.location_id AND r.id=m.root_id WHERE m.location_id=x.location_id AND m.media_file_id=x.media_file_id AND m.enabled AND r.enabled)`, lease.LocationID)
	return int(tag.RowsAffected()), err
}
