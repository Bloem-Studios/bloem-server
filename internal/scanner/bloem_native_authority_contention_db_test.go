//go:build integration

package scanner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// A run/binding has one outstanding claim; two independent legal claims cannot
// coexist. Reuse that real claim to exercise the production post-wait recheck.
func TestNativeAuthorityPublisherBindingContentionDB(t *testing.T) {
	x := nativeIngestSetup(t) // includes nativeIngestDatabase's owned UUID clone
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	var account int
	var org, folderOwner, platform uuid.UUID
	if err := x.pool.QueryRow(ctx, "INSERT INTO users(username,email,password_hash,role) VALUES('publisher-contention','publisher-contention@example.test','fixture','admin') RETURNING id").Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := x.pool.QueryRow(ctx, "INSERT INTO organizations(slug,name,status,owner_account_id) VALUES('publisher-contention','Publisher contention','active',$1) RETURNING id", account).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := x.pool.QueryRow(ctx, "SELECT id FROM resource_owners WHERE organization_id=$1", org).Scan(&folderOwner); err != nil {
		t.Fatal(err)
	}
	if err := x.pool.QueryRow(ctx, "SELECT bloem_platform_resource_owner_id()").Scan(&platform); err != nil {
		t.Fatal(err)
	}
	var installation int64
	if err := x.pool.QueryRow(ctx, "INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id,kind,enabled) VALUES('fixture','1','/fixture',$1,'plugin',true) RETURNING id", platform).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	nativeIngestSQL(t, x.pool, "INSERT INTO bloem_storage_installations(installation_id,owner_id,protocol_version) VALUES($1,$2,1)", installation, platform)
	nativeIngestSQL(t, x.pool, "UPDATE bloem_storage_sources SET owner_id=$2,installation_id=$3 WHERE key=$1", x.source.Key, platform, installation)
	// Remove only the compatibility grant generated for this new fixture folder
	// before assigning its organization owner (the composite FK retains ownership).
	nativeIngestSQL(t, x.pool, "DELETE FROM organization_entitlements WHERE media_folder_id=$1 AND granted_by_service='resource-root-compatibility'", x.folder.ID)
	nativeIngestSQL(t, x.pool, "UPDATE media_folders SET owner_id=$2,enabled=true WHERE id=$1", x.folder.ID, folderOwner)
	nativeIngestSQL(t, x.pool, "INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) VALUES($1,'plugin_availability','plugin_installation',$2,$3,'active','publisher-contention-test')", org, platform, installation)
	x.source.OwnerID, x.source.InstallationID = platform, &installation
	authority := resourcetenancy.NewStore(x.pool)

	// Independent readers retain exactly the parsed EPUB bytes and pinned identity.
	data := make([]byte, x.file.Reader.Size())
	if _, err := x.file.Reader.ReadAt(data, 0); err != nil {
		t.Fatal(err)
	}
	secondFile := nativeBytes(x.file.info.Name, data)
	secondFile.info = x.file.info
	type result struct {
		id  string
		err error
	}
	aDone, bDone := make(chan result, 1), make(chan result, 1)
	aAuthorized, bStarted, bAuthorized := make(chan uint32, 1), make(chan uint32, 1), make(chan struct{}, 1)
	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	workers.Add(1)
	go func() {
		defer workers.Done()
		id, err := x.s.PublishAuthorizedNativeEbook(ctx, x.r, x.claim, x.folder, x.file, NativeEbookSidecars{Complete: true}, func(ctx context.Context, tx pgx.Tx) error {
			if err := authority.RequireNativeScanTx(ctx, tx, x.binding, x.source); err != nil {
				return err
			}
			aAuthorized <- tx.Conn().PgConn().PID()
			select {
			case <-releaseA:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		aDone <- result{id, err}
	}()
	var aPID uint32
	select {
	case aPID = <-aAuthorized:
	case r := <-aDone:
		t.Fatalf("A failed before policy barrier: %v", r.err)
	case <-ctx.Done():
		t.Fatal("A policy barrier timed out")
	}
	x.empty(t)
	var retainedToken uuid.UUID
	var retainedPending, retainedRevision, retainedLast string
	if err := x.pool.QueryRow(ctx, "SELECT pending_token,pending_entry_id,pending_revision,last_entry_id FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2", x.claim.Lease.RunID, x.binding.ID).Scan(&retainedToken, &retainedPending, &retainedRevision, &retainedLast); err != nil || retainedToken != x.claim.Token || retainedPending != x.claim.Entry.Id || retainedRevision != x.claim.Entry.Revision || retainedLast != "" {
		t.Fatalf("claim changed before publication: %v", err)
	}

	// An independent transaction proves A retains the real binding SHARE lock
	// before any catalog callback attempts to upgrade it to UPDATE.
	probe, err := x.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = probe.Exec(ctx, "SELECT id FROM bloem_storage_bindings WHERE id=$1 FOR UPDATE NOWAIT", x.binding.ID)
	_ = probe.Rollback(ctx)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("binding policy lock not retained: %v", err)
	}

	workers.Add(1)
	go func() {
		defer workers.Done()
		id, err := x.s.PublishAuthorizedNativeEbook(ctx, x.r, x.claim, x.folder, secondFile, NativeEbookSidecars{Complete: true}, func(ctx context.Context, tx pgx.Tx) error {
			bStarted <- tx.Conn().PgConn().PID()
			if err := authority.RequireNativeScanTx(ctx, tx, x.binding, x.source); err != nil {
				return err
			}
			bAuthorized <- struct{}{}
			select {
			case <-releaseB:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		bDone <- result{id, err}
	}()
	var bPID uint32
	select {
	case bPID = <-bStarted:
	case r := <-bDone:
		t.Fatalf("B failed before authority: %v", r.err)
	case <-ctx.Done():
		t.Fatal("B start timed out")
	}
	observeCtx, stopObserve := context.WithTimeout(ctx, 5*time.Second)
	defer stopObserve()
	for {
		var blocked bool
		err := x.pool.QueryRow(observeCtx, `SELECT $2::int=ANY(pg_blocking_pids(pid)) AND wait_event_type='Lock'
   AND query LIKE '%bloem_storage_sources%FOR UPDATE%' FROM pg_stat_activity WHERE pid=$1`, int(bPID), int(aPID)).Scan(&blocked)
		if err != nil {
			t.Fatalf("B source contention not observed within bound: %v", err)
		}
		if blocked {
			break
		}
		select {
		case r := <-bDone:
			t.Fatalf("B completed before contention: %v", r.err)
		default:
		}
	}
	t.Log("observed B blocked by A at actual source FOR UPDATE; A retains binding SHARE before catalog")
	close(releaseA)
	var a result
	select {
	case a = <-aDone:
	case <-ctx.Done():
		t.Fatal("A binding upgrade/commit did not complete within bound")
	}
	if a.err != nil || a.id == "" {
		t.Fatalf("A binding upgrade/publication failed: id=%q err=%v", a.id, a.err)
	}
	select {
	case <-bAuthorized:
	case r := <-bDone:
		t.Fatalf("B did not resume authority: %v", r.err)
	case <-ctx.Done():
		t.Fatal("B authority did not resume within bound")
	}

	// B is now authorized but held before its source/run/checkpoint recheck.
	// Snapshot all relevant committed rows, then prove its stale claim changes none.
	snapshot := func() string {
		t.Helper()
		var state string
		err := x.pool.QueryRow(ctx, `SELECT jsonb_build_object(
   'items',(SELECT jsonb_agg(to_jsonb(i) ORDER BY content_id) FROM media_items i WHERE content_id=$1),
   'files',(SELECT jsonb_agg(to_jsonb(f) ORDER BY id) FROM media_files f WHERE media_folder_id=$2),
   'refs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY media_file_id) FROM bloem_storage_file_refs r WHERE binding_id=$3),
   'libraries',(SELECT jsonb_agg(to_jsonb(l) ORDER BY content_id) FROM media_item_libraries l WHERE media_folder_id=$2),
   'checkpoint',(SELECT to_jsonb(c) FROM bloem_storage_ingestion c WHERE run_id=$4 AND binding_id=$3))::text`, a.id, x.folder.ID, x.binding.ID, x.claim.Lease.RunID).Scan(&state)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := snapshot()
	close(releaseB)
	var b result
	select {
	case b = <-bDone:
	case <-ctx.Done():
		t.Fatal("B stale claim rejection did not complete within bound")
	}
	if b.id != "" || !errors.Is(b.err, storagesource.ErrCheckpointConflict) {
		t.Fatalf("B did not reject consumed claim: id=%q err=%v", b.id, b.err)
	}
	if after := snapshot(); after != before {
		t.Fatal("stale B overwrote committed catalog/ref/checkpoint state")
	}

	var files, refs, items, libraries int
	if err := x.pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM media_files WHERE media_folder_id=$1),
 (SELECT count(*) FROM bloem_storage_file_refs WHERE binding_id=$2),
 (SELECT count(*) FROM media_items WHERE title='The Test Ebook'),
 (SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1)`, x.folder.ID, x.binding.ID).Scan(&files, &refs, &items, &libraries); err != nil || files != 1 || refs != 1 || items != 1 || libraries != 1 {
		t.Fatalf("duplicate/partial publication files=%d refs=%d items=%d libraries=%d: %v", files, refs, items, libraries, err)
	}
	var contentID, location, root, format, probeSource, entry, revision, logicalPath string
	if err := x.pool.QueryRow(ctx, `SELECT f.content_id,f.file_path,f.canonical_root_path,f.container,f.probe_source,r.entry_id,r.revision,r.logical_path
 FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE f.media_folder_id=$1 AND r.binding_id=$2`, x.folder.ID, x.binding.ID).Scan(&contentID, &location, &root, &format, &probeSource, &entry, &revision, &logicalPath); err != nil {
		t.Fatal(err)
	}
	expectedLocation, err := storagesource.CatalogLocation(x.binding.ID, x.claim.Entry.Id)
	if err != nil || contentID != a.id || location != expectedLocation || root != expectedLocation || format != "epub" || probeSource != "native" || entry != x.claim.Entry.Id || revision != x.claim.Entry.Revision || logicalPath != x.claim.Entry.LogicalPath {
		t.Fatalf("wrong committed native file/ref identity: %v", err)
	}
	item, err := x.s.itemRepo.GetByID(ctx, a.id)
	if err != nil || item.Title != "The Test Ebook" || item.Year == 0 || item.OriginalLanguage == "" {
		t.Fatalf("real parsed metadata missing: %+v %v", item, err)
	}
	var last, pending, pendingRevision string
	var token *uuid.UUID
	var complete bool
	var epoch int64
	if err := x.pool.QueryRow(ctx, "SELECT last_entry_id,pending_entry_id,pending_revision,pending_token,complete,lease_epoch FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2", x.claim.Lease.RunID, x.binding.ID).Scan(&last, &pending, &pendingRevision, &token, &complete, &epoch); err != nil || last != x.claim.Entry.Id || pending != "" || pendingRevision != "" || token != nil || complete || epoch != x.claim.Lease.Epoch {
		t.Fatalf("wrong committed checkpoint last=%q pending=%q revision=%q tokenNil=%v complete=%v epoch=%d: %v", last, pending, pendingRevision, token == nil, complete, epoch, err)
	}
	t.Log("A SHARE-to-UPDATE upgrade committed parsed EPUB, one file/ref/library, and consumed checkpoint; B resumed real authority, rejected checkpoint conflict, and preserved identical committed state")
}
