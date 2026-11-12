package bookrequests

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// nativeStorageLibrary is a Bookwarehouse storage installation with one
// library reading from it.
type nativeStorageLibrary struct {
	installation int64
	folder       int
	source       storagesource.SourceConfig
	location     uuid.UUID
	root         uuid.UUID
}

func nativeStorageLibraryFixture(t *testing.T, pool *pgxpool.Pool, kind, name string) nativeStorageLibrary {
	t.Helper()
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var n nativeStorageLibrary
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id) VALUES($1,'1','/fixture',$2) RETURNING id`, StorageRequestPluginID, owner).Scan(&n.installation); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, n.installation, owner)
	source, err := storagesource.NewRepository(pool).CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: owner, InstallationID: &n.installation, PluginID: StorageRequestPluginID, ProviderSourceID: kind, RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	n.source = source
	locations := nativestorage.NewLibraryLocations(pool)
	locations.SetNativeAudioReady(true)
	folder, err := catalog.NewFolderRepository(pool).Create(t.Context(), catalog.CreateFolderInput{Type: kind, Name: name, AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error {
		return locations.AttachTx(ctx, tx, source.Key, id)
	}})
	if err != nil {
		t.Fatal(err)
	}
	n.folder = folder.ID
	if err = pool.QueryRow(t.Context(), `SELECT id FROM library_storage_locations WHERE folder_id=$1`, n.folder).Scan(&n.location); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(t.Context(), `SELECT id FROM bloem_storage_selected_roots WHERE location_id=$1 LIMIT 1`, n.location).Scan(&n.root); errors.Is(err, pgx.ErrNoRows) {
		n.root = uuid.New()
		bookSQL(t, pool, `INSERT INTO bloem_storage_selected_roots(id,location_id,directory_id,name,logical_path) VALUES($1,$2,'root','','')`, n.root, n.location)
	} else if err != nil {
		t.Fatal(err)
	}
	return n
}

// requestIntegration adds the storage plugin's request_router capability and
// a connection that, like every one the setup creates for it, carries no URL
// or key: the plugin uses its installation's own connection.
func (n nativeStorageLibrary) requestIntegration(t *testing.T, pool *pgxpool.Pool, capability, mediaType string) string {
	t.Helper()
	bookSQL(t, pool, `INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id) VALUES($1,'request_router.v1',$2)`, n.installation, capability)
	id := strconv.FormatInt(n.installation*1000000+7, 10)
	bookSQL(t, pool, `INSERT INTO request_integrations(id,name,enabled,base_url,api_key_ref,installation_id,capability_id,supported_media_types,plugin_config) VALUES($1,'Bookwarehouse',true,'','',$2,$3,ARRAY[$4],'{}')`, id, n.installation, capability, mediaType)
	return id
}

// publishTrack publishes one audiobook track the storage plugin names
// "audio/<audiobook>/<file>", the way native audio ingestion records it.
func (n nativeStorageLibrary) publishTrack(t *testing.T, pool *pgxpool.Pool, content, audiobook string, file int, configurationRevision int64) {
	t.Helper()
	entry := "audio/" + audiobook + "/" + strconv.Itoa(file)
	path, err := storagesource.CatalogLocation(n.location, entry)
	if err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO media_items(content_id,type,title) VALUES($1,'audiobook','Synthetic Saga') ON CONFLICT DO NOTHING`, content)
	var id int64
	if err = pool.QueryRow(t.Context(), `INSERT INTO media_files(media_folder_id,file_path,content_id) VALUES($1,$2,$3) RETURNING id`, n.folder, path, content).Scan(&id); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO bloem_storage_file_refs(media_file_id,location_id,entry_id,revision,logical_path,configuration_revision) VALUES($1,$2,$3,'r1',$3,$4)`, id, n.location, entry, configurationRevision)
	bookSQL(t, pool, `INSERT INTO bloem_storage_file_roots(location_id,media_file_id,root_id) VALUES($1,$2,$3)`, n.location, id, n.root)
}

// The storage plugin serves ebook requests for the library its own
// installation stores, with no URL or key of its own.
func TestEbookStorageRequestPluginBindingDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	v.Admin = true
	books := nativeStorageLibraryFixture(t, pool, "ebooks", "Electronic Books")
	other := nativeStorageLibraryFixture(t, pool, "ebooks", "Other Books")
	id := books.requestIntegration(t, pool, EbookStorageCapabilityID, "book")

	bindings, err := s.GetEbookIntegrationLibraries(t.Context(), v)
	if err != nil || len(bindings) != 1 || bindings[0].ID != id || bindings[0].InstallationID != int(books.installation) || !bindings[0].Native {
		t.Fatalf("storage plugin integration not listed: %+v %v", bindings, err)
	}
	if _, err = s.SetEbookIntegrationLibrary(t.Context(), v, id, other.folder); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bound to another installation's library: %v", err)
	}
	bound, err := s.SetEbookIntegrationLibrary(t.Context(), v, id, books.folder)
	if err != nil || bound.LibraryID != books.folder || !bound.Native || bound.StorageSourceKey != books.source.Key {
		t.Fatalf("own library binding: %+v %v", bound, err)
	}
	if err = s.ValidateEbookIntegration(t.Context(), bound); err != nil {
		t.Fatalf("live native descriptor denied: %v", err)
	}

	// A binding whose library moves to another installation's source no
	// longer serves.
	bookSQL(t, pool, `UPDATE bloem_storage_sources SET installation_id=$2 WHERE key=$1`, books.source.Key, other.installation)
	if err = s.ValidateEbookIntegration(t.Context(), bound); !errors.Is(err, ErrConflict) {
		t.Fatalf("binding to a foreign installation's library still valid: %v", err)
	}
	bookSQL(t, pool, `UPDATE bloem_storage_sources SET installation_id=$2 WHERE key=$1`, books.source.Key, books.installation)

	// Only the ebooks capability serves the ebook lane.
	bookSQL(t, pool, `UPDATE request_integrations SET capability_id='audiobooks' WHERE id=$1`, id)
	bookSQL(t, pool, `INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id) VALUES($1,'request_router.v1','audiobooks')`, books.installation)
	if bindings, err = s.GetEbookIntegrationLibraries(t.Context(), v); err != nil || len(bindings) != 0 {
		t.Fatalf("audiobooks capability serves ebooks: %+v %v", bindings, err)
	}
	bookSQL(t, pool, `UPDATE request_integrations SET capability_id='ebooks' WHERE id=$1`, id)

	// Dispatch freezes the native descriptor and completes from the library.
	r := approveEbook(t, s, v, "9780306406157")
	var frozen []EbookIntegration
	s.SetEbookFulfillment(ebookRouterFixture{fulfill: func(_ context.Context, in DispatchInput) (Receipt, error) {
		frozen = append(frozen, in.Integration)
		return Receipt{ExternalID: "provider-receipt", Status: "completed"}, nil
	}, recover: func(context.Context, DispatchInput) (Receipt, error) {
		return Receipt{}, errors.New("unexpected recovery")
	}, status: func(context.Context, DispatchInput, Receipt) (ProviderStatus, error) {
		return ProviderStatus{Status: "completed"}, nil
	}})
	if _, err = s.ReconcileEbooks(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	if len(frozen) != 1 || frozen[0] != bound {
		t.Fatalf("dispatch descriptor %+v, want %+v", frozen, bound)
	}
	publishEbookCatalogFixture(t, pool, books.source, books.folder, "native-edition", r.ISBN, "native-edition")
	dueEbook(t, pool, r.ID)
	if n, err := s.ReconcileEbooks(t.Context(), 100); err != nil || n != 1 {
		t.Fatalf("native library file did not complete the request: %d %v", n, err)
	}
}

func bookwarehouseReceipt(in AudiobookDispatchInput) Receipt {
	raw, _ := json.Marshal(AudiobookReceipt{Kind: "qbittorrent", ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", TorrentID: "6f1c0e8a-3b2d-4c1e-9f7a-0123456789ab", RequestID: in.SubmissionID, Attempt: in.Attempt, Client: "0123456789abcdef", Confirmed: true, Sel: "Synthetic Saga", ExpMB: 512})
	return Receipt{ExternalID: "abr1." + base64.RawURLEncoding.EncodeToString(raw), Status: "queued"}
}

// The storage plugin downloads into Bookwarehouse, so its binding has no
// download folders; a verified download completes the request through the
// native library entry of the Bookwarehouse audiobook it became, even when
// the request's identifiers match nothing in the library.
func TestAudiobookNativeCompletionByAudiobookIDDatabase(t *testing.T) {
	_, v, pool := databaseFixture(t)
	env := &audiobookEnv{v: v, pool: pool, fake: &audiobookFake{}, notices: &audiobookNoticeSpy{}}
	env.s = NewService(pool, env.notices)
	env.s.SetAudiobookFulfillment(env.fake)
	env.admin = v
	env.admin.Admin = true
	audio := nativeStorageLibraryFixture(t, pool, "audiobooks", "Audio books")
	other := nativeStorageLibraryFixture(t, pool, "audiobooks", "Other audio")
	local := audiobookFolder(t, pool, v.OrganizationID, "Local audio", "audiobooks", "/library/audiobooks")
	id := audio.requestIntegration(t, pool, AudiobookStorageCapabilityID, "audiobook")

	for label, tc := range map[string]struct {
		folder int
		roots  []string
	}{"local library": {local, nil}, "other installation": {other.folder, nil}, "download folders": {audio.folder, []string{"/library/audiobooks/requests"}}} {
		if _, err := env.s.SetAudiobookIntegrationLibrary(t.Context(), env.admin, id, tc.folder, tc.roots); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: binding accepted: %v", label, err)
		}
	}
	summary, err := env.s.SetAudiobookIntegrationLibrary(t.Context(), env.admin, id, audio.folder, nil)
	if err != nil || !summary.Ready || !summary.Native || len(summary.DownloadRoots) != 0 {
		t.Fatalf("native binding: %+v %v", summary, err)
	}
	listed, err := env.s.ListAudiobookIntegrations(t.Context(), env.admin)
	if err != nil || len(listed) != 1 || !listed[0].Ready || !listed[0].Native || listed[0].LibraryID != audio.folder {
		t.Fatalf("native integration listing: %+v %v", listed, err)
	}

	const audiobook = "6f1c0e8a-3b2d-4c1e-9f7a-00000000beef"
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		out := handedOff(in, "abb:/abss/synthetic-saga/")
		out.Receipt = bookwarehouseReceipt(in)
		return out, nil
	}
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Fixture Author", ISBN: "9780306406157"})
	env.reconcile(t)
	in := env.fake.lastFulfill(t)
	if !in.Integration.Native || in.Integration.LibraryID != audio.folder || len(in.Integration.DownloadRoots) != 0 {
		t.Fatalf("dispatched integration %+v", in.Integration)
	}
	verified := &AcquisitionDecision{V: 1, Phase: "verify", Outcome: "verified", AudiobookID: audiobook}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("completed", "verified", verified), nil
	}
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 0 {
		t.Fatalf("completed before the library has the audiobook: %d", n)
	}
	if got := env.get(t, r.ID); got.Status != "approved" || got.Fulfilment != "awaiting_library" {
		t.Fatalf("verified download state %+v", got)
	}

	// Another audiobook's tracks, a stale reference and a track in another
	// installation's library do not complete it.
	audio.publishTrack(t, pool, "other-book", "6f1c0e8a-3b2d-4c1e-9f7a-00000000cafe", 1, 1)
	audio.publishTrack(t, pool, "stale-book", audiobook, 1, 99)
	other.publishTrack(t, pool, "foreign-book", audiobook, 2, 1)
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 0 {
		t.Fatalf("unrelated library entry completed the request: %d", n)
	}

	audio.publishTrack(t, pool, "native-book", audiobook, 3, 1)
	audio.publishTrack(t, pool, "native-book", audiobook, 4, 1)
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 1 {
		t.Fatalf("native audiobook did not complete the request: %d", n)
	}
	got := env.get(t, r.ID)
	if got.Status != "completed" || got.Fulfilment != "library" || got.LibraryContentID != "native-book" {
		t.Fatalf("completion %+v", got)
	}
	if fulfills, checks, _ := env.fake.counts(); fulfills != 1 || checks != 1 {
		t.Fatalf("provider calls after verification: fulfill %d check %d", fulfills, checks)
	}
}
