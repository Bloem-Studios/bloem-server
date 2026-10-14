package bookrequests

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/scanner"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ebookRouterFixture struct {
	fulfill func(context.Context, DispatchInput) (Receipt, error)
	recover func(context.Context, DispatchInput) (Receipt, error)
	status  func(context.Context, DispatchInput, Receipt) (ProviderStatus, error)
}

func (f ebookRouterFixture) Fulfill(ctx context.Context, in DispatchInput) (Receipt, error) {
	return f.fulfill(ctx, in)
}
func (f ebookRouterFixture) Recover(ctx context.Context, in DispatchInput) (Receipt, error) {
	return f.recover(ctx, in)
}
func (f ebookRouterFixture) CheckStatus(ctx context.Context, in DispatchInput, receipt Receipt) (ProviderStatus, error) {
	return f.status(ctx, in, receipt)
}

func ebookIntegrationFixture(t *testing.T, pool *pgxpool.Pool) (string, int, storagesource.SourceConfig) {
	t.Helper()
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var storageID, routerID int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id) VALUES('bloem.storage.bookwarehouse','1','/fixture',$1) RETURNING id`, owner).Scan(&storageID); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, storageID, owner)
	source, err := storagesource.NewRepository(pool).CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, InstallationID: &storageID, PluginID: "bloem.storage.bookwarehouse", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	folder, err := catalog.NewFolderRepository(pool).Create(t.Context(), catalog.CreateFolderInput{Type: "ebooks", Name: "Bookwarehouse", AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error {
		return nativestorage.NewLibraryLocations(pool).AttachTx(ctx, tx, source.Key, id)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id) VALUES('bloem.requests.bookwarehouse','1','/fixture',$1) RETURNING id`, owner).Scan(&routerID); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id) VALUES($1,'request_router.v1','bookwarehouse')`, routerID)
	id := strconv.FormatInt(routerID*1000000, 10)
	bookSQL(t, pool, `INSERT INTO request_integrations(id,name,enabled,base_url,api_key_ref,installation_id,capability_id,supported_media_types,plugin_config) VALUES($1,'Bookwarehouse',true,'https://backend.example','fixture-reference',$2,'bookwarehouse',ARRAY['book'],'{}')`, id, routerID)
	return id, folder.ID, source
}

func approveEbook(t *testing.T, s *Service, v Viewer, isbn string) Request {
	t.Helper()
	r, _, err := s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Edition", ISBN: isbn}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	v.Admin = true
	r, err = s.ActEbook(t.Context(), v, r.ID, "approve", ActionInput{ExpectedRevision: r.Revision})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func dueEbook(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	bookSQL(t, pool, `UPDATE bloem_ebook_request_dispatches SET next_attempt_at=now()-interval '1 second' WHERE request_id=$1`, id)
}

func publishEbookCatalogFixture(t *testing.T, pool *pgxpool.Pool, source storagesource.SourceConfig, folderID int, content, isbn, entry string) (int, string) {
	t.Helper()
	var location uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM library_storage_locations WHERE folder_id=$1`, folderID).Scan(&location); err != nil {
		t.Fatal(err)
	}
	path, err := storagesource.CatalogLocation(location, entry)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.New()
	bookSQL(t, pool, `INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until) VALUES($1,$2,1,'complete',1,'fixture',now())`, run, source.Key)
	bookSQL(t, pool, `INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run) VALUES($1,$2,'Edition.epub',$2,1,10,0,'fixture-version',1,$3)`, source.Key, entry, run)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	e := &storagev1.Entry{Id: entry, Name: "Edition.epub", LogicalPath: entry, Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 10, Revision: "fixture-version", Ebook: &storagev1.EbookMetadata{Title: "Edition", Isbn: isbn}}
	book, ok := scanner.StorageEbookFromEntry(e)
	if !ok {
		t.Fatal("metadata not accepted")
	}
	publisher := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0)
	if _, err = publisher.PublishStorageEbooksTx(t.Context(), tx, &models.MediaFolder{ID: folderID, Type: "ebooks", Name: "Bookwarehouse", Enabled: true}, storagesource.Location{ID: location, SourceKey: source.Key, FolderID: folderID, SelectionRevision: 1}, 1, []scanner.StorageEbook{book}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var file int
	if err = pool.QueryRow(t.Context(), `SELECT id FROM media_files WHERE media_folder_id=$1 AND file_path=$2`, folderID, path).Scan(&file); err != nil {
		t.Fatal(err)
	}
	return file, path
}

func TestEbookCompletionRequiresExactLinkedNativeFileDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	integration, folder, source := ebookIntegrationFixture(t, pool)
	admin := v
	admin.Admin = true
	if _, err := s.SetEbookIntegrationLibrary(t.Context(), admin, integration, folder); err != nil {
		t.Fatal(err)
	}
	r := approveEbook(t, s, v, "9780306406157")
	var submits atomic.Int32
	s.SetEbookFulfillment(ebookRouterFixture{fulfill: func(context.Context, DispatchInput) (Receipt, error) {
		submits.Add(1)
		return Receipt{ExternalID: "provider-receipt", Status: "completed"}, nil
	}, recover: func(context.Context, DispatchInput) (Receipt, error) {
		return Receipt{}, errors.New("unexpected recovery")
	}, status: func(context.Context, DispatchInput, Receipt) (ProviderStatus, error) {
		return ProviderStatus{}, errors.New("completed backend was queried again")
	}})
	if _, err := s.ReconcileEbooks(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	_, otherFolder, otherSource := ebookIntegrationFixture(t, pool)
	publishEbookCatalogFixture(t, pool, otherSource, otherFolder, "other-library", r.ISBN, "other-edition")
	dueEbook(t, pool, r.ID)
	if n, err := s.ReconcileEbooks(t.Context(), 100); err != nil || n != 0 {
		t.Fatalf("other library completed request: %d %v", n, err)
	}
	file, path := publishEbookCatalogFixture(t, pool, source, folder, "linked-edition", "0306406152", "linked-edition")
	var linkedID string
	if err := pool.QueryRow(t.Context(), `SELECT content_id FROM media_files WHERE id=$1`, file).Scan(&linkedID); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `UPDATE media_files SET file_path='/fixture/replaced.epub' WHERE id=$1`, file)
	dueEbook(t, pool, r.ID)
	if n, err := s.ReconcileEbooks(t.Context(), 100); err != nil || n != 0 {
		t.Fatalf("replaced filesystem path completed native request: %d %v", n, err)
	}
	bookSQL(t, pool, `UPDATE media_files SET file_path=$2,missing_since=now() WHERE id=$1`, file, path)
	dueEbook(t, pool, r.ID)
	if n, err := s.ReconcileEbooks(t.Context(), 100); err != nil || n != 0 {
		t.Fatalf("missing file completed request: %d %v", n, err)
	}
	bookSQL(t, pool, `UPDATE media_files SET missing_since=NULL WHERE id=$1`, file)
	dueEbook(t, pool, r.ID)
	if n, err := s.ReconcileEbooks(t.Context(), 100); err != nil || n != 1 {
		t.Fatalf("exact linked native file did not complete: %d %v", n, err)
	}
	got, err := s.GetEbook(t.Context(), v, r.ID)
	if err != nil || got.Status != "completed" || got.Fulfilment != "library" || got.LibraryContentID != linkedID || got.CompletedAt == nil || submits.Load() != 1 {
		t.Fatalf("incorrect completion: %+v submits=%d %v", got, submits.Load(), err)
	}
}

func TestEbookBookwarehouseBindingDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	integrationID, folderID, _ := ebookIntegrationFixture(t, pool)
	if _, err := s.SetEbookIntegrationLibrary(t.Context(), v, integrationID, folderID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin bound backend: %v", err)
	}
	v.Admin = true
	bindings, err := s.GetEbookIntegrationLibraries(t.Context(), v)
	if err != nil || len(bindings) != 1 || bindings[0].ID != integrationID || bindings[0].LibraryID != 0 {
		t.Fatalf("eligible unbound integration: %+v %v", bindings, err)
	}
	bound, err := s.SetEbookIntegrationLibrary(t.Context(), v, integrationID, folderID)
	if err != nil || bound.LibraryID != folderID || bound.ConfigurationRevision == "" || bound.BindingRevision <= 0 {
		t.Fatalf("explicit binding: %+v %v", bound, err)
	}
	same, err := s.SetEbookIntegrationLibrary(t.Context(), v, integrationID, folderID)
	if err != nil || same != bound {
		t.Fatalf("saving unchanged binding invalidated dispatch: %+v %v", same, err)
	}
	if err := s.ValidateEbookIntegration(t.Context(), bound); err != nil {
		t.Fatalf("live descriptor denied: %v", err)
	}
	libraries, err := s.EbookLibraries(t.Context(), v)
	if err != nil || len(libraries) != 1 || libraries[0].ID != folderID {
		t.Fatalf("eligible library list: %+v %v", libraries, err)
	}
	bookSQL(t, pool, `UPDATE request_integrations SET supported_media_types='{}' WHERE id=$1`, integrationID)
	if err := s.ValidateEbookIntegration(t.Context(), bound); !errors.Is(err, ErrConflict) {
		t.Fatalf("empty wildcard retained authority: %v", err)
	}
	bindings, err = s.GetEbookIntegrationLibraries(t.Context(), v)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("wildcard appears as ebook backend: %+v %v", bindings, err)
	}
	bookSQL(t, pool, `UPDATE request_integrations SET supported_media_types=ARRAY['book'] WHERE id=$1`, integrationID)
	bookSQL(t, pool, `UPDATE plugin_installations SET plugin_id='other.storage' WHERE id=(SELECT installation_id FROM bloem_storage_sources WHERE key=(SELECT source_key FROM library_storage_locations WHERE folder_id=$1))`, folderID)
	if _, err := s.SetEbookIntegrationLibrary(t.Context(), v, integrationID, folderID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign storage provider accepted: %v", err)
	}
}

func TestEbookDispatchReceiptAndAmbiguousRecoveryDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	integrationID, folderID, _ := ebookIntegrationFixture(t, pool)
	admin := v
	admin.Admin = true
	if _, err := s.SetEbookIntegrationLibrary(t.Context(), admin, integrationID, folderID); err != nil {
		t.Fatal(err)
	}
	r := approveEbook(t, s, v, "9780306406157")
	var submits, recoveries atomic.Int32
	s.SetEbookFulfillment(ebookRouterFixture{
		fulfill: func(_ context.Context, in DispatchInput) (Receipt, error) {
			submits.Add(1)
			if in.Request.ID != r.ID || in.IdempotencyKey != r.ID || in.Integration.LibraryID != folderID {
				return Receipt{}, errors.New("wrong dispatch identity")
			}
			return Receipt{}, errors.New("submission response lost")
		},
		recover: func(_ context.Context, in DispatchInput) (Receipt, error) {
			recoveries.Add(1)
			return Receipt{ExternalID: "unknown:" + in.IdempotencyKey, Status: "unknown"}, nil
		},
		status: func(_ context.Context, in DispatchInput, receipt Receipt) (ProviderStatus, error) {
			if receipt.ExternalID != "unknown:"+in.IdempotencyKey {
				return ProviderStatus{}, errors.New("lost durable receipt")
			}
			return ProviderStatus{Status: "completed"}, nil
		},
	})
	for i := 0; i < 3; i++ {
		if _, err := s.ReconcileEbooks(t.Context(), 100); err != nil {
			t.Fatal(err)
		}
		dueEbook(t, pool, r.ID)
	}
	got, err := s.GetEbook(t.Context(), v, r.ID)
	if err != nil || got.Status != "approved" || got.Fulfilment != "awaiting_library" || got.LibraryContentID != "" {
		t.Fatalf("provider complete bypassed actual library: %+v %v", got, err)
	}
	if submits.Load() != 1 || recoveries.Load() != 1 {
		t.Fatalf("ambiguous result blindly resubmitted: submissions=%d recoveries=%d", submits.Load(), recoveries.Load())
	}
	var receipt string
	if err := pool.QueryRow(t.Context(), `SELECT external_id FROM bloem_ebook_request_dispatches WHERE request_id=$1`, r.ID).Scan(&receipt); err != nil || receipt != "unknown:"+r.ID {
		t.Fatalf("receipt not durable: %q %v", receipt, err)
	}
	bookSQL(t, pool, `UPDATE bloem_ebook_request_dispatches SET failure_count=5 WHERE request_id=$1`, r.ID)
	bookSQL(t, pool, `UPDATE bloem_book_requests SET fulfilment='failed',revision=revision+1 WHERE id=$1`, r.ID)
	got, err = s.GetEbook(t.Context(), v, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ActEbook(t.Context(), admin, r.ID, "retry", ActionInput{ExpectedRevision: got.Revision}); err != nil {
		t.Fatalf("admin cannot retry existing submission checks: %v", err)
	}
	if _, err = s.ReconcileEbooks(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	if submits.Load() != 1 || recoveries.Load() != 1 {
		t.Fatal("retry discarded started marker or immutable receipt")
	}
}

func TestEbookDispatchLeaseAndLateResponseFencesDatabase(t *testing.T) {
	for _, mutation := range []string{"cancel", "opt_out", "opt_out_on", "global_off_on", "integration_disable", "rebind", "unbind_rebind", "lease_expiry", "requester_disable"} {
		t.Run(mutation, func(t *testing.T) {
			s, v, pool := databaseFixture(t)
			setEbookOptIn(t, pool, "true")
			integration, folder, _ := ebookIntegrationFixture(t, pool)
			admin := v
			admin.Admin = true
			if _, err := s.SetEbookIntegrationLibrary(t.Context(), admin, integration, folder); err != nil {
				t.Fatal(err)
			}
			r := approveEbook(t, s, v, "9780306406157")
			started, release, done := make(chan DispatchInput, 1), make(chan struct{}), make(chan error, 1)
			var releaseOnce sync.Once
			var submissions atomic.Int32
			router := ebookRouterFixture{fulfill: func(ctx context.Context, in DispatchInput) (Receipt, error) {
				submissions.Add(1)
				started <- in
				select {
				case <-release:
					return Receipt{ExternalID: "late:" + in.IdempotencyKey, Status: "completed"}, nil
				case <-ctx.Done():
					return Receipt{}, ctx.Err()
				}
			}, recover: func(context.Context, DispatchInput) (Receipt, error) {
				return Receipt{}, errors.New("unexpected duplicate recovery while lease live")
			}, status: func(context.Context, DispatchInput, Receipt) (ProviderStatus, error) {
				return ProviderStatus{Status: "completed"}, nil
			}}
			s.SetEbookFulfillment(router)
			callCtx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			joined := false
			defer func() {
				releaseOnce.Do(func() { close(release) })
				if !joined {
					select {
					case <-done:
					case <-callCtx.Done():
						t.Error("dispatch did not join")
					}
				}
			}()
			go func() { _, err := s.ReconcileEbooks(callCtx, 100); done <- err }()
			select {
			case in := <-started:
				if in.Request.ID != r.ID || in.IdempotencyKey != r.ID {
					t.Fatal("wrong retained dispatch identity")
				}
			case <-callCtx.Done():
				t.Fatal("provider did not start")
			}
			other := NewService(pool, nil)
			other.SetEbookFulfillment(router)
			if n, err := other.ReconcileEbooks(callCtx, 100); err != nil || n != 0 || submissions.Load() != 1 {
				t.Fatalf("second replica duplicated live dispatch: %d submits=%d %v", n, submissions.Load(), err)
			}
			// These writes must finish while provider I/O is blocked. A database
			// transaction spanning that I/O would block cancellation or setup.
			mutationCtx, mutationCancel := context.WithTimeout(callCtx, 3*time.Second)
			switch mutation {
			case "cancel":
				if _, err := s.ActEbook(mutationCtx, v, r.ID, "cancel", ActionInput{ExpectedRevision: r.Revision}); err != nil {
					t.Fatal(err)
				}
			case "opt_out":
				setEbookOptIn(t, pool, "false")
			case "opt_out_on":
				setEbookOptIn(t, pool, "false")
				setEbookOptIn(t, pool, "true")
			case "global_off_on":
				bookSQL(t, pool, `UPDATE request_settings SET requests_enabled=false`)
				bookSQL(t, pool, `UPDATE request_settings SET requests_enabled=true`)
			case "integration_disable":
				bookSQL(t, pool, `UPDATE request_integrations SET enabled=false WHERE id=$1`, integration)
			case "rebind":
				_, newFolder, _ := ebookIntegrationFixture(t, pool)
				if _, err := s.SetEbookIntegrationLibrary(mutationCtx, admin, integration, newFolder); err != nil {
					t.Fatal(err)
				}
			case "unbind_rebind":
				if _, err := s.SetEbookIntegrationLibrary(mutationCtx, admin, integration, 0); err != nil {
					t.Fatal(err)
				}
				if _, err := s.SetEbookIntegrationLibrary(mutationCtx, admin, integration, folder); err != nil {
					t.Fatal(err)
				}
			case "lease_expiry":
				bookSQL(t, pool, `UPDATE bloem_ebook_request_dispatches SET lease_until=now()-interval '1 second' WHERE request_id=$1`, r.ID)
			case "requester_disable":
				bookSQL(t, pool, `UPDATE users SET enabled=false WHERE id=$1`, v.UserID)
			}
			mutationCancel()
			releaseOnce.Do(func() { close(release) })
			select {
			case err := <-done:
				joined = true
				if err != nil {
					t.Fatal(err)
				}
			case <-callCtx.Done():
				t.Fatal("provider outcome did not join")
			}
			got, err := scan(pool.QueryRow(t.Context(), `SELECT `+selectColumns+` FROM bloem_book_requests WHERE id=$1`, r.ID))
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "approved" || got.Fulfilment != "awaiting_fulfilment" || got.LibraryContentID != "" {
				t.Fatalf("late provider outcome crossed authority fence: %+v", got)
			}
			if mutation == "cancel" && got.Outcome != "cancelled" {
				t.Fatal("local cancellation lost")
			}
			var receipt string
			if err := pool.QueryRow(t.Context(), `SELECT external_id FROM bloem_ebook_request_dispatches WHERE request_id=$1`, r.ID).Scan(&receipt); err != nil || receipt != "late:"+r.ID {
				t.Fatalf("fenced outcome lost immutable receipt: %q %v", receipt, err)
			}
			if mutation == "lease_expiry" {
				dueEbook(t, pool, r.ID)
				if _, err := other.ReconcileEbooks(t.Context(), 100); err != nil {
					t.Fatal(err)
				}
				if submissions.Load() != 1 {
					t.Fatal("expired lease resubmitted accepted acquisition")
				}
			}
		})
	}
}

func TestEbookUnknownChecksStopAndRetryPreservesReceiptDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	integration, folder, _ := ebookIntegrationFixture(t, pool)
	admin := v
	admin.Admin = true
	if _, err := s.SetEbookIntegrationLibrary(t.Context(), admin, integration, folder); err != nil {
		t.Fatal(err)
	}
	r := approveEbook(t, s, v, "9780306406157")
	var submissions, checks atomic.Int32
	s.SetEbookFulfillment(ebookRouterFixture{fulfill: func(context.Context, DispatchInput) (Receipt, error) {
		submissions.Add(1)
		return Receipt{ExternalID: "retained-unknown", Status: "unknown"}, nil
	}, recover: func(context.Context, DispatchInput) (Receipt, error) {
		return Receipt{}, errors.New("retained receipt was discarded")
	}, status: func(context.Context, DispatchInput, Receipt) (ProviderStatus, error) {
		checks.Add(1)
		return ProviderStatus{Status: "unknown"}, nil
	}})
	capability, err := s.CapabilityEbook(t.Context(), v)
	if err != nil || capability.FulfilmentBackend != "bookwarehouse" || capability.ManualFulfilment {
		t.Fatalf("configured capability: %+v %v", capability, err)
	}
	for range 8 {
		if _, err := s.ReconcileEbooks(t.Context(), 100); err != nil {
			t.Fatal(err)
		}
		dueEbook(t, pool, r.ID)
	}
	got, err := s.GetEbook(t.Context(), v, r.ID)
	if err != nil || got.Status != "approved" || got.Fulfilment != "failed" || got.Outcome != "active" || submissions.Load() != 1 || checks.Load() != 4 {
		t.Fatalf("unknown checks not bounded: %+v submits=%d checks=%d %v", got, submissions.Load(), checks.Load(), err)
	}
	if _, err := s.ActEbook(t.Context(), v, r.ID, "retry", ActionInput{ExpectedRevision: got.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin retried: %v", err)
	}
	if _, err := s.ActEbook(t.Context(), admin, r.ID, "retry", ActionInput{ExpectedRevision: got.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileEbooks(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	if submissions.Load() != 1 || checks.Load() != 5 {
		t.Fatal("retry resubmitted acquisition instead of checking immutable receipt")
	}
}

func TestEbookConfiguredSiblingSurvivesInvalidBindingDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	invalidID, invalidFolder, _ := ebookIntegrationFixture(t, pool)
	validID, validFolder, _ := ebookIntegrationFixture(t, pool)
	admin := v
	admin.Admin = true
	for id, folder := range map[string]int{invalidID: invalidFolder, validID: validFolder} {
		if _, err := s.SetEbookIntegrationLibrary(t.Context(), admin, id, folder); err != nil {
			t.Fatal(err)
		}
	}
	bookSQL(t, pool, `UPDATE media_folders SET enabled=false WHERE id=$1`, invalidFolder)
	bindings, err := s.GetEbookIntegrationLibraries(t.Context(), admin)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("binding list: %+v %v", bindings, err)
	}
	for _, binding := range bindings {
		if binding.ID == invalidID && binding.LibraryID != invalidFolder {
			t.Fatal("admin lost stored invalid library binding")
		}
	}
	s.SetEbookFulfillment(ebookRouterFixture{fulfill: func(_ context.Context, in DispatchInput) (Receipt, error) {
		if in.Integration.ID != validID || in.Integration.LibraryID != validFolder {
			t.Error("dispatch selected invalid sibling")
		}
		return Receipt{ExternalID: "valid-sibling", Status: "queued"}, nil
	}})
	capability, err := s.CapabilityEbook(t.Context(), v)
	if err != nil || capability.FulfilmentBackend != "bookwarehouse" {
		t.Fatalf("valid sibling hidden: %+v %v", capability, err)
	}
	r := approveEbook(t, s, v, "9780306406157")
	if _, err := s.ReconcileEbooks(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEbook(t.Context(), v, r.ID)
	if err != nil || got.Fulfilment != "queued" {
		t.Fatalf("valid sibling did not dispatch: %+v %v", got, err)
	}
}

func TestEbookCancelledRefilingAdoptsInflightSubmissionDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	integration, folder, _ := ebookIntegrationFixture(t, pool)
	admin := v
	admin.Admin = true
	if _, err := s.SetEbookIntegrationLibrary(t.Context(), admin, integration, folder); err != nil {
		t.Fatal(err)
	}
	first := approveEbook(t, s, v, "9780306406157")
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	var submissions, recoveries atomic.Int32
	router := ebookRouterFixture{fulfill: func(ctx context.Context, in DispatchInput) (Receipt, error) {
		submissions.Add(1)
		if in.Request.ID != first.ID {
			return Receipt{}, errors.New("refiled request issued another POST")
		}
		close(started)
		select {
		case <-release:
			return Receipt{ExternalID: "original-receipt", Status: "queued"}, nil
		case <-ctx.Done():
			return Receipt{}, ctx.Err()
		}
	}, recover: func(_ context.Context, in DispatchInput) (Receipt, error) {
		recoveries.Add(1)
		if in.IdempotencyKey != first.ID {
			return Receipt{}, errors.New("original uncertain submission identity lost")
		}
		return Receipt{ExternalID: "unknown:" + first.ID, Status: "unknown"}, nil
	}, status: func(context.Context, DispatchInput, Receipt) (ProviderStatus, error) {
		return ProviderStatus{Status: "queued"}, nil
	}}
	s.SetEbookFulfillment(router)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	joined := false
	defer func() {
		once.Do(func() { close(release) })
		if !joined {
			select {
			case <-done:
			case <-ctx.Done():
				t.Error("original dispatch did not join")
			}
		}
	}()
	go func() { _, err := s.ReconcileEbooks(ctx, 100); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("original submit did not start")
	}
	if _, err := s.ActEbook(ctx, v, first.ID, "cancel", ActionInput{ExpectedRevision: first.Revision}); err != nil {
		t.Fatal(err)
	}
	second := approveEbook(t, s, v, "0306406152")
	other := NewService(pool, nil)
	other.SetEbookFulfillment(router)
	if _, err := other.ReconcileEbooks(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if submissions.Load() != 1 || recoveries.Load() != 1 {
		t.Fatalf("refile duplicated in-flight acquisition: submits=%d recoveries=%d", submissions.Load(), recoveries.Load())
	}
	var origin string
	if err := pool.QueryRow(ctx, `SELECT submission_request_id FROM bloem_ebook_request_dispatches WHERE request_id=$1`, second.ID).Scan(&origin); err != nil || origin != first.ID {
		t.Fatalf("submission lineage not durable: %s %v", origin, err)
	}
	once.Do(func() { close(release) })
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("original submit did not join")
	}
	dueEbook(t, pool, second.ID)
	if _, err := other.ReconcileEbooks(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if submissions.Load() != 1 {
		t.Fatal("refile later resubmitted")
	}
}

func TestEbookPreRPCFailureDoesNotPoisonSubmissionDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	integration, folder, _ := ebookIntegrationFixture(t, pool)
	admin := v
	admin.Admin = true
	if _, err := s.SetEbookIntegrationLibrary(t.Context(), admin, integration, folder); err != nil {
		t.Fatal(err)
	}
	r := approveEbook(t, s, v, "9780306406157")
	calls, recoveries := 0, 0
	s.SetEbookFulfillment(ebookRouterFixture{fulfill: func(context.Context, DispatchInput) (Receipt, error) {
		calls++
		if calls == 1 {
			return Receipt{}, errors.Join(ErrProviderNotCalled, errors.New("plugin not running"))
		}
		return Receipt{ExternalID: "submitted", Status: "queued"}, nil
	}, recover: func(context.Context, DispatchInput) (Receipt, error) {
		recoveries++
		return Receipt{}, errors.New("must not recover an unsent request")
	}})
	if _, err := s.ReconcileEbooks(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	var started bool
	if err := pool.QueryRow(t.Context(), `SELECT started FROM bloem_ebook_request_dispatches WHERE request_id=$1`, r.ID).Scan(&started); err != nil || started {
		t.Fatalf("unsent submission left started=%v err=%v", started, err)
	}
	// The requester must not be told the outcome is unknown: nothing was
	// sent, and the next attempt submits again.
	if got, err := s.GetEbook(t.Context(), v, r.ID); err != nil || got.Fulfilment != "retrying" || got.OutcomeReason != "The submission did not reach the backend; it will be submitted again." {
		t.Fatalf("unsent retry reason: %+v %v", got, err)
	}
	dueEbook(t, pool, r.ID)
	if _, err := s.ReconcileEbooks(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || recoveries != 0 {
		t.Fatalf("calls=%d recoveries=%d", calls, recoveries)
	}
	got, err := s.GetEbook(t.Context(), v, r.ID)
	if err != nil || got.Fulfilment != "queued" {
		t.Fatalf("request %+v %v", got, err)
	}
}

func TestEbookRepeatedPreRPCFailureEndsWithResubmittingRetryDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	integration, folder, _ := ebookIntegrationFixture(t, pool)
	admin := v
	admin.Admin = true
	if _, err := s.SetEbookIntegrationLibrary(t.Context(), admin, integration, folder); err != nil {
		t.Fatal(err)
	}
	r := approveEbook(t, s, v, "9780306406157")
	calls := 0
	s.SetEbookFulfillment(ebookRouterFixture{fulfill: func(context.Context, DispatchInput) (Receipt, error) {
		calls++
		return Receipt{}, errors.Join(ErrProviderNotCalled, errors.New("ebook fulfillment configuration rejected"))
	}, recover: func(context.Context, DispatchInput) (Receipt, error) {
		return Receipt{}, errors.New("must not recover an unsent request")
	}})
	for range ebookMaxFailures {
		if _, err := s.ReconcileEbooks(t.Context(), 100); err != nil {
			t.Fatal(err)
		}
		dueEbook(t, pool, r.ID)
	}
	got, err := s.GetEbook(t.Context(), v, r.ID)
	if err != nil || calls != ebookMaxFailures || got.Fulfilment != "failed" || got.OutcomeReason != "Submission failed repeatedly before reaching the backend; retry submits it again." {
		t.Fatalf("calls=%d request %+v %v", calls, got, err)
	}
}

func TestBookRequestDefaultInitializingAuthorityDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	bookSQL(t, pool, `UPDATE organizations SET status='initializing' WHERE id=$1 AND is_default`, v.OrganizationID)
	setEbookOptIn(t, pool, "true")
	integration, folder, _ := ebookIntegrationFixture(t, pool)
	admin := v
	admin.Admin = true
	libraries, err := s.EbookLibraries(t.Context(), admin)
	if err != nil || len(libraries) != 1 || libraries[0].ID != folder {
		t.Fatalf("default initializing library admission: %+v %v", libraries, err)
	}
	if _, err = s.SetEbookIntegrationLibrary(t.Context(), admin, integration, folder); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Synthetic Edition", ISBN: "9780306406157"}, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Create(t.Context(), v, CreateInput{Title: "Synthetic Saga", ASIN: "B012345678"}, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}
