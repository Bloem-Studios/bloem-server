//go:build integration

package resourcetenancy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

func aggregateFixture(t *testing.T, mode string) (*nativeScanFixture, NativeResourceSet) {
	t.Helper()
	x := newNativeScanFixture(t, mode)
	nativeScanSQL(t, x.pool, `UPDATE plugin_installations SET runtime_generation=1 WHERE id=$1`, x.source.InstallationID)
	nativeScanSQL(t, x.pool, `INSERT INTO bloem_storage_selected_roots(id,location_id,directory_id,name,logical_path) VALUES($1,$2,'root','root','') ON CONFLICT(location_id,directory_id) DO NOTHING`, uuid.New(), x.binding.ID)
	selections, err := x.repo.LocationSelections(t.Context(), x.binding.FolderID)
	if err != nil {
		t.Fatal(err)
	}
	return x, NativeResourceSet{Sources: []NativeSourceExpectation{{Source: x.source, Generation: 1}}, Library: &NativeLibraryExpectation{FolderID: x.binding.FolderID, Locations: selections}}
}
func TestNativeAggregateRetainsMixedLibraryDB(t *testing.T) {
	x, set := aggregateFixture(t, "platform")
	nativeScanSQL(t, x.pool, `INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,'/synthetic/local')`, x.binding.FolderID)
	var installation int64
	if err := x.pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id,runtime_generation) VALUES('aggregate-second','1','/synthetic',$1,1) RETURNING id`, x.source.OwnerID).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	nativeScanSQL(t, x.pool, `INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, installation, x.source.OwnerID)
	other, err := x.repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: x.source.OwnerID, InstallationID: &installation, PluginID: "aggregate-second", ProviderSourceID: "other", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	nativeScanSQL(t, x.pool, `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,$3)`, uuid.New(), other.Key, x.binding.FolderID)
	set.Sources = append(set.Sources, NativeSourceExpectation{Source: other, Generation: 1})
	set.Library.Locations = nil
	tx, err := x.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	retained, err := x.store.RetainNativeResourcesTx(t.Context(), tx, nil, set)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained.Sources) != 2 || len(retained.Locations) != 2 || retained.LibraryKind != "ebooks" {
		t.Fatalf("aggregate evidence: %+v", retained)
	}
}
func TestNativeAggregateRejectsChangesAfterInstallationWaitDB(t *testing.T) {
	for _, effect := range []string{"disable", "uninstall", "configuration", "generation", "library-delete", "root-change", "entitlement"} {
		t.Run(effect, func(t *testing.T) {
			mode := "platform"
			if effect == "entitlement" {
				mode = "entitled"
			}
			x, set := aggregateFixture(t, mode)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			holder, err := x.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback(context.Background())
			var id int64
			if err := holder.QueryRow(ctx, `SELECT id FROM plugin_installations WHERE id=$1 FOR UPDATE`, x.source.InstallationID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			started := make(chan int, 1)
			go func() {
				tx, err := x.pool.Begin(ctx)
				if err != nil {
					result <- err
					return
				}
				defer tx.Rollback(context.Background())
				started <- int(tx.Conn().PgConn().PID())
				_, err = x.store.RetainNativeResourcesTx(ctx, tx, nil, set)
				result <- err
			}()
			worker := <-started
			poll := time.NewTicker(time.Millisecond)
			defer poll.Stop()
			for {
				var blocked bool
				if err := x.pool.QueryRow(ctx, `SELECT $2::integer=ANY(pg_blocking_pids($1))`, worker, int(holder.Conn().PgConn().PID())).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case <-poll.C:
				case err := <-result:
					t.Fatalf("missing installation wait: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			// No owner or source may be retained while the aggregate waits for an installation.
			var owner uuid.UUID
			if err := holder.QueryRow(ctx, `SELECT id FROM resource_owners WHERE id=$1 FOR UPDATE NOWAIT`, x.source.OwnerID).Scan(&owner); err != nil {
				t.Fatal("aggregate retained owner before all installations", err)
			}
			switch effect {
			case "disable":
				_, err = holder.Exec(ctx, `UPDATE plugin_installations SET enabled=false WHERE id=$1`, id)
			case "uninstall":
				_, err = holder.Exec(ctx, `UPDATE bloem_storage_sources SET installation_id=NULL,enabled=false,configuration_revision=configuration_revision+1 WHERE key=$1`, x.source.Key)
			case "configuration":
				_, err = holder.Exec(ctx, `UPDATE bloem_storage_sources SET configuration_revision=configuration_revision+1 WHERE key=$1`, x.source.Key)
			case "generation":
				_, err = holder.Exec(ctx, `UPDATE plugin_installations SET runtime_generation=runtime_generation+1 WHERE id=$1`, id)
			case "library-delete":
				_, err = holder.Exec(ctx, `DELETE FROM organization_entitlements WHERE media_folder_id=$1`, x.binding.FolderID)
				if err == nil {
					_, err = holder.Exec(ctx, `DELETE FROM media_folders WHERE id=$1`, x.binding.FolderID)
				}
			case "root-change":
				_, err = holder.Exec(ctx, `UPDATE bloem_storage_selected_roots SET enabled=false WHERE location_id=$1`, x.binding.ID)
			case "entitlement":
				_, err = holder.Exec(ctx, `UPDATE organization_entitlements SET status='revoked',revoked_at=clock_timestamp() WHERE plugin_installation_id=$1`, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := holder.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if !errors.Is(err, ErrResourceHidden) && !errors.Is(err, ErrAuthorizationStateChanged) {
					t.Fatalf("changed authority accepted: %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

func TestNativeAggregateLocksEveryInstallationBeforeOwnersDB(t *testing.T) {
	x, set := aggregateFixture(t, "platform")
	var id int64
	if err := x.pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id,runtime_generation) VALUES('aggregate-second','1','/synthetic',$1,1) RETURNING id`, x.source.OwnerID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	nativeScanSQL(t, x.pool, `INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, id, x.source.OwnerID)
	other, err := x.repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: x.source.OwnerID, InstallationID: &id, PluginID: "aggregate-second", ProviderSourceID: "second", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// Reverse request order deliberately; locks must still use installation IDs.
	set.Sources = append([]NativeSourceExpectation{{Source: other, Generation: 1}}, set.Sources...)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	holder, err := x.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err := holder.Exec(ctx, `SELECT id FROM plugin_installations WHERE id=$1 FOR NO KEY UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	started := make(chan int, 1)
	go func() {
		tx, err := x.pool.Begin(ctx)
		if err != nil {
			result <- err
			return
		}
		defer tx.Rollback(context.Background())
		started <- int(tx.Conn().PgConn().PID())
		_, err = x.store.RetainNativeResourcesTx(ctx, tx, nil, set)
		result <- err
	}()
	worker := <-started
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		var blocked bool
		if err := x.pool.QueryRow(ctx, `SELECT $2::integer=ANY(pg_blocking_pids($1))`, worker, int(holder.Conn().PgConn().PID())).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-poll.C:
		case err := <-result:
			t.Fatalf("missing second installation wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if _, err := holder.Exec(ctx, `SELECT id FROM resource_owners WHERE id=$1 FOR UPDATE NOWAIT`, x.source.OwnerID); err != nil {
		t.Fatal("owner acquired before second installation", err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
func TestNativeAggregateAllowsRemovalOfUnavailableExistingSourceDB(t *testing.T) {
	for _, effect := range []string{"disabled", "detached", "revoked"} {
		t.Run(effect, func(t *testing.T) {
			x, set := aggregateFixture(t, "entitled")
			set.Sources = nil
			switch effect {
			case "disabled":
				nativeScanSQL(t, x.pool, `UPDATE plugin_installations SET enabled=false WHERE id=$1`, x.source.InstallationID)
			case "detached":
				nativeScanSQL(t, x.pool, `UPDATE bloem_storage_sources SET installation_id=NULL,enabled=false,configuration_revision=configuration_revision+1 WHERE key=$1`, x.source.Key)
			case "revoked":
				nativeScanSQL(t, x.pool, `UPDATE organization_entitlements SET status='revoked',revoked_at=clock_timestamp() WHERE plugin_installation_id=$1`, x.source.InstallationID)
			}
			tx, err := x.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			out, err := x.store.RetainNativeResourcesTx(t.Context(), tx, nil, set)
			if err != nil {
				t.Fatal("removal blocked by unavailable source", err)
			}
			if len(out.Sources) != 1 || len(out.Locations) != 1 {
				t.Fatal("removal lost retained rows")
			}
		})
	}
}
