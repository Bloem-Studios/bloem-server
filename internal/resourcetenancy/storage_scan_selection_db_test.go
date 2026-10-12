//go:build integration

package resourcetenancy

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStorageAttachmentDoesNotRequireRunningGenerationDB(t *testing.T) {
	for _, mode := range []string{"platform", "same-org", "entitled", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			x := newNativeScanFixture(t, mode)
			nativeScanSQL(t, x.pool, `UPDATE plugin_installations SET runtime_generation=0 WHERE id=$1`, *x.source.InstallationID)
			tx, err := x.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			err = x.store.RequireStorageScanTx(t.Context(), tx, x.binding, x.source)
			if mode == "foreign" {
				if !errors.Is(err, ErrResourceHidden) {
					t.Fatalf("foreign attachment accepted: %v", err)
				}
			} else if err != nil {
				t.Fatalf("legacy attachment requires running provider: %v", err)
			}
			if err = tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err = x.store.RequireStorageScan(t.Context(), x.binding, x.source); !errors.Is(err, ErrResourceHidden) {
				t.Fatalf("background scan accepted generation zero: %v", err)
			}
		})
	}
}

func TestStorageScanRetainsCompleteMixedGraphDB(t *testing.T) {
	x, _ := aggregateFixture(t, "platform")
	nativeScanSQL(t, x.pool, `INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,'/synthetic/local')`, x.binding.FolderID)
	nativeScanSQL(t, x.pool, `INSERT INTO bloem_storage_selected_roots(id,location_id,directory_id,name,logical_path) VALUES($1,$2,'child','child','')`, uuid.New(), x.binding.ID)
	other, err := x.repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: x.source.OwnerID, InstallationID: x.source.InstallationID, PluginID: x.source.PluginID, ProviderSourceID: "other", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	nativeScanSQL(t, x.pool, `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,$3)`, uuid.New(), other.Key, x.binding.FolderID)
	if err = x.store.RequireStorageScan(t.Context(), x.binding, x.source); err != nil {
		t.Fatalf("mixed background authority rejected: %v", err)
	}
	pinned, err := x.store.StorageScanResources(t.Context(), x.binding, x.source)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned.Library.Locations) != 2 || len(pinned.Library.Locations[0].Roots)+len(pinned.Library.Locations[1].Roots) != 2 {
		t.Fatalf("incomplete graph: %+v", pinned.Library)
	}
	// Background scans still require current typed-owner policy.
	nativeScanSQL(t, x.pool, `UPDATE resource_owners SET revision=revision+1 WHERE id=$1`, x.source.OwnerID)
	if err = x.store.RequireStorageScan(t.Context(), x.binding, x.source); err != nil {
		t.Fatal(err)
	}
}
func TestStorageScanPinnedGraphRejectsOtherLocationEditDB(t *testing.T) {
	x, _ := aggregateFixture(t, "platform")
	other, err := x.repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: x.source.OwnerID, InstallationID: x.source.InstallationID, PluginID: x.source.PluginID, ProviderSourceID: "other", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	otherLocation := uuid.New()
	nativeScanSQL(t, x.pool, `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,$3)`, otherLocation, other.Key, x.binding.FolderID)
	nativeScanSQL(t, x.pool, `INSERT INTO bloem_storage_selected_roots(id,location_id,directory_id,name,logical_path) VALUES($1,$2,'root','root','')`, uuid.New(), otherLocation)
	pinned, err := x.store.StorageScanResources(t.Context(), x.binding, x.source)
	if err != nil {
		t.Fatal(err)
	}
	nativeScanSQL(t, x.pool, `UPDATE bloem_storage_selected_roots SET name='changed' WHERE location_id=$1`, otherLocation)
	tx, err := x.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = x.store.RequireStorageScanTx(t.Context(), tx, x.binding, x.source, pinned); !errors.Is(err, ErrResourceHidden) {
		t.Fatalf("pinned graph edit accepted: %v", err)
	}
}

func TestStorageScanPinnedGraphRechecksInstallationWaitDB(t *testing.T) {
	x, _ := aggregateFixture(t, "platform")
	pinned, err := x.store.StorageScanResources(t.Context(), x.binding, x.source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	holder, err := x.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err = holder.Exec(ctx, `SELECT id FROM plugin_installations WHERE id=$1 FOR UPDATE`, *x.source.InstallationID); err != nil {
		t.Fatal(err)
	}
	pids := make(chan int, 1)
	result := make(chan error, 1)
	go func() {
		tx, err := x.pool.Begin(ctx)
		if err != nil {
			result <- err
			return
		}
		defer tx.Rollback(context.Background())
		var pid int
		if err = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			result <- err
			return
		}
		pids <- pid
		result <- x.store.RequireStorageScanTx(ctx, tx, x.binding, x.source, pinned)
	}()
	var pid int
	select {
	case pid = <-pids:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		var waiting bool
		if err = x.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err = <-result:
			t.Fatalf("did not retain installation before graph: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-poll.C:
		}
	}
	if _, err = holder.Exec(ctx, `UPDATE bloem_storage_selected_roots SET enabled=false WHERE location_id=$1`, x.binding.ID); err != nil {
		t.Fatal(err)
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, ErrResourceHidden) {
			t.Fatalf("wait retained revoked graph: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// journalContention has two libraries whose graphs both retain sources A and
// B; library i scans source i. Every journal transaction retains its pinned
// aggregate (source SHARE, then folder UPDATE) before the journal verifier.
type journalContention struct {
	pool    *pgxpool.Pool
	store   *Store
	repo    *storagesource.Repository
	sources [2]storagesource.SourceConfig
	scans   [2]storagesource.Location
	pins    [2]NativeResourceSet
	leases  [2]storagesource.Lease
	cps     [2]storagesource.Checkpoint
}

func newJournalContention(t *testing.T) *journalContention {
	t.Helper()
	pool := nativeScanDatabase(t)
	x := &journalContention{pool: pool, store: NewStore(pool), repo: storagesource.NewRepository(pool)}
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var installation int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id,runtime_generation) VALUES('journal-contention','1','/synthetic',$1,1) RETURNING id`, owner).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	nativeScanSQL(t, pool, `INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, installation, owner)
	for i := range x.sources {
		source, err := x.repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, InstallationID: &installation, PluginID: "journal-contention", ProviderSourceID: fmt.Sprintf("source-%d", i), RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		x.sources[i] = source
	}
	for i := range x.scans {
		var folder int
		if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name,owner_id) VALUES('ebooks',$1,$2) RETURNING id`, fmt.Sprintf("Journal contention %d", i), owner).Scan(&folder); err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for j, source := range x.sources {
			location, err := x.repo.AddLocationTx(t.Context(), tx, source.Key, folder)
			if err != nil {
				_ = tx.Rollback(context.Background())
				t.Fatal(err)
			}
			if i == j {
				x.scans[i] = location
			}
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for i := range x.scans {
		var err error
		if x.pins[i], err = x.store.StorageScanResources(t.Context(), x.scans[i], x.sources[i]); err != nil {
			t.Fatal(err)
		}
		if x.leases[i], err = x.repo.BeginLocationAuthorized(t.Context(), x.scans[i], fmt.Sprintf("worker-%d", i), time.Minute, x.authorize(i, nil, nil)); err != nil {
			t.Fatal(err)
		}
		var more bool
		if x.cps[i], more, err = x.repo.NextDirectory(t.Context(), x.leases[i]); err != nil || !more {
			t.Fatalf("initial checkpoint %d: %v", i, err)
		}
	}
	return x
}

// authorize is the production scan authorizer. started reports the backend
// before any lock; retained runs after the complete aggregate is retained.
func (x *journalContention) authorize(i int, started func(int), retained func(context.Context) error) storagesource.BeginAuthorizer {
	return func(ctx context.Context, tx pgx.Tx) error {
		if started != nil {
			var pid int
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				return err
			}
			started(pid)
		}
		if err := x.store.RequireStorageScanTx(ctx, tx, x.scans[i], x.sources[i], x.pins[i]); err != nil {
			return err
		}
		if retained != nil {
			return retained(ctx)
		}
		return nil
	}
}

// run performs one production journal operation of scan i. scan-entry is the
// ScanReader admission transaction: retained aggregate, then pending page.
func (x *journalContention) run(ctx context.Context, op string, i int, authorize storagesource.BeginAuthorizer) error {
	switch op {
	case "page":
		return x.repo.ApplyPageAuthorized(ctx, x.leases[i], x.cps[i], &storagev1.ListResponse{Complete: true}, storagesource.PageAuthorizer(authorize), nil)
	case "renewal":
		return x.repo.RenewAuthorized(ctx, x.leases[i], time.Minute, authorize)
	case "completion":
		_, err := x.repo.CompleteLocationAuthorized(ctx, x.leases[i], storagesource.CompletionAuthorizer(authorize), nil)
		return err
	case "scan-entry":
		tx, err := x.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if err = authorize(ctx, tx); err != nil {
			return err
		}
		if err = storagesource.ValidatePendingPageTx(ctx, tx, x.leases[i], x.cps[i]); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	return fmt.Errorf("unknown journal operation %q", op)
}

func journalDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// One scan's periodic renewal overlaps its own page and ScanReader admission.
// The holder retains the aggregate (source SHARE, folder UPDATE); the other
// transaction is observed holding source SHARE and waiting for that folder.
// Upgrading the holder's source lock then would close a 40P01 cycle.
func TestStorageScanSameScanJournalLockOrderDB(t *testing.T) {
	for _, pair := range [][2]string{{"page", "renewal"}, {"scan-entry", "renewal"}, {"renewal", "page"}, {"renewal", "scan-entry"}} {
		t.Run(pair[0]+"-holds-"+pair[1]+"-waits", func(t *testing.T) {
			x := newJournalContention(t)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			holderPID := make(chan int, 1)
			retained := make(chan struct{})
			release := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				results <- x.run(ctx, pair[0], 0, x.authorize(0, func(pid int) { holderPID <- pid }, func(ctx context.Context) error {
					close(retained)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}))
			}()
			var holder int
			select {
			case holder = <-holderPID:
			case err := <-results:
				t.Fatalf("holder returned before retaining: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case <-retained:
			case err := <-results:
				t.Fatalf("holder returned before retaining: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			waiterPID := make(chan int, 1)
			go func() {
				results <- x.run(ctx, pair[1], 0, x.authorize(0, func(pid int) { waiterPID <- pid }, nil))
			}()
			var waiter int
			select {
			case waiter = <-waiterPID:
			case err := <-results:
				t.Fatalf("waiter returned before its authority wait: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			poll := time.NewTicker(5 * time.Millisecond)
			defer poll.Stop()
			for {
				var blocked bool
				if err := x.pool.QueryRow(ctx, `SELECT $2::integer=ANY(pg_blocking_pids($1))`, waiter, holder).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case <-poll.C:
				case err := <-results:
					t.Fatalf("%s did not wait for the retained folder: %v", pair[1], err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			// The aggregate retains sorted sources FOR SHARE before the folder, so
			// a waiter at the folder lock already holds the holder's source SHARE.
			var atFolder bool
			if err := x.pool.QueryRow(ctx, `SELECT query LIKE '%FROM media_folders WHERE id=$1 FOR UPDATE%' FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock'`, waiter).Scan(&atFolder); err != nil {
				t.Fatal(err)
			}
			if !atFolder {
				t.Fatalf("%s is not waiting at the retained folder lock; the regression would not form the reviewed cycle", pair[1])
			}
			close(release)
			for range 2 {
				select {
				case err := <-results:
					if journalDeadlock(err) {
						t.Fatalf("%s/%s journal lock cycle: %v", pair[0], pair[1], err)
					}
					if err != nil {
						t.Fatalf("%s/%s: %v", pair[0], pair[1], err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		})
	}
}

// Two libraries retain the same two sources and scan different ones. Both
// transactions retain their complete graphs before either verifies its lease.
func TestStorageScanOverlappingLibrariesJournalLockOrderDB(t *testing.T) {
	for _, pair := range [][2]string{{"page", "page"}, {"renewal", "renewal"}, {"completion", "scan-entry"}, {"scan-entry", "completion"}} {
		t.Run(pair[0]+"-"+pair[1], func(t *testing.T) {
			x := newJournalContention(t)
			for i, op := range pair {
				if op == "completion" {
					if err := x.run(t.Context(), "page", i, x.authorize(i, nil, nil)); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			retained := make(chan int, 2)
			release := make(chan struct{})
			results := make(chan error, 2)
			for i, op := range pair {
				go func() {
					err := x.run(ctx, op, i, x.authorize(i, nil, func(ctx context.Context) error {
						retained <- i
						select {
						case <-release:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}))
					if err != nil {
						err = fmt.Errorf("library %d %s: %w", i, op, err)
					}
					results <- err
				}()
			}
			for range 2 {
				select {
				case <-retained:
				case err := <-results:
					t.Fatalf("returned before both graphs were retained: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			close(release)
			for range 2 {
				select {
				case err := <-results:
					if journalDeadlock(err) {
						t.Fatalf("overlapping journal lock cycle: %v", err)
					}
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		})
	}
}
