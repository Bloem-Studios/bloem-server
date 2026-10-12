//go:build integration

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/metadata"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// task3LibraryDatabase clones the disposable storage template named by
// SILO_TEST_DATABASE_URL. Like the native-storage fixtures, it refuses any
// database without the disposable prefix, wherever the server listens.
func task3LibraryDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "bloem_storage_test_") {
		t.Fatal("refusing non-disposable Task3 database")
	}
	adminCfg := cfg.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminCfg)
	if err != nil {
		t.Fatal(err)
	}
	name := "bloem_storage_test_task3_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{cfg.ConnConfig.Database}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH(FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	return pool
}
func TestLibraryCreationAttachmentFilesystemParityDB(t *testing.T) {
	pool := task3LibraryDatabase(t)
	var installation int
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,enabled,kind) VALUES('task3.metadata','1','/synthetic',true,'plugin') RETURNING id`).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id,metadata) VALUES($1,'metadata_provider.v1','task3.metadata','{"default_priority":{"movie":1},"default_enabled":true}')`, installation); err != nil {
		t.Fatal(err)
	}
	repo := catalog.NewFolderRepository(pool)
	h := NewLibraryHandler(repo, nil, nil, pool, nil)
	h.SectionRepo = sections.NewRepository(pool)
	h.ChainRepo = metadata.NewChainRepository(pool)
	h.ScanQueue = noopLibraryScanQueue{}
	monitor := &countingMonitorPoker{}
	h.RealtimeMonitor = monitor
	normal, err := h.CreateLibrary(t.Context(), LibraryCreateRequest{Name: "Parity", Type: "movies", Paths: []string{" /media/ordinary/../ordinary "}})
	if err != nil {
		t.Fatal(err)
	}
	extension, err := h.CreateLibraryWithAttachment(t.Context(), LibraryCreateRequest{Name: "Parity", Type: "movies", Paths: []string{" /media/attached/../attached "}}, func(context.Context, pgx.Tx, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if monitor.pokes.Load() != 2 {
		t.Fatal("creation monitor follow-up lost")
	}
	if normal.MetadataLanguage != "en" || normal.MetadataLanguage != extension.MetadataLanguage || !normal.RealtimeMonitoring || !extension.RealtimeMonitoring || !reflect.DeepEqual(normal.TrailerKinds, extension.TrailerKinds) || len(extension.Paths) != 1 || extension.Paths[0] != "/media/attached" {
		t.Fatalf("defaults differ: %+v %+v", normal, extension)
	}
	ordinarySections, err := h.SectionRepo.ListByScopeAll(t.Context(), "library", &normal.ID)
	if err != nil {
		t.Fatal(err)
	}
	attachedSections, err := h.SectionRepo.ListByScopeAll(t.Context(), "library", &extension.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinarySections) == 0 || len(ordinarySections) != len(attachedSections) {
		t.Fatal("section defaults lost")
	}
	for i, s := range ordinarySections {
		other := attachedSections[i]
		if s.SectionType != other.SectionType || s.Title != other.Title || s.ItemLimit != other.ItemLimit || s.Enabled != other.Enabled {
			t.Fatal("section behavior differs")
		}
	}
	chain, err := h.ChainRepo.GetChain(t.Context(), normal.ID, "movie")
	if err != nil {
		t.Fatal(err)
	}
	attachedChain, err := h.ChainRepo.GetChain(t.Context(), extension.ID, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) == 0 || !reflect.DeepEqual(chain, attachedChain) {
		t.Fatalf("provider defaults differ: %+v %+v", chain, attachedChain)
	}
	var groups int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM library_collection_groups WHERE library_id=ANY($1) AND kind='user_collections'`, []int{normal.ID, extension.ID}).Scan(&groups); err != nil || groups != 2 {
		t.Fatalf("canonical collections lost count=%d err=%v", groups, err)
	}
	changed := []string{"/media/updated"}
	if _, err := h.UpdateLibrary(t.Context(), extension.ID, 0, LibraryUpdateRequest{Paths: &changed}); err != nil {
		t.Fatal(err)
	}
	f, err := repo.GetByID(t.Context(), extension.ID)
	if err != nil || !reflect.DeepEqual(f.Paths, changed) {
		t.Fatalf("legacy update parity %v %v", f, err)
	}
}

// recordingLibraryScanQueue records every follow-up scan the handlers request.
type recordingLibraryScanQueue struct {
	mu    sync.Mutex
	scans []string
}

func (q *recordingLibraryScanQueue) record(scan string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.scans = append(q.scans, scan)
}
func (q *recordingLibraryScanQueue) take() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	scans := q.scans
	q.scans = nil
	return scans
}
func (q *recordingLibraryScanQueue) EnqueueLibraryScan(_ context.Context, folderID int, trigger string) (bool, error) {
	q.record(fmt.Sprintf("%d:%s", folderID, trigger))
	return true, nil
}
func (q *recordingLibraryScanQueue) EnqueueScan(_ context.Context, folderID int, mode, path, trigger string) (bool, error) {
	q.record(fmt.Sprintf("%d:%s:%s:%s", folderID, mode, path, trigger))
	return true, nil
}
func (q *recordingLibraryScanQueue) CancelAcceptedByLibrary(context.Context, int) (int, error) {
	return 0, nil
}
func (q *recordingLibraryScanQueue) CancelByLibrary(context.Context, int) (int, error) { return 0, nil }

type readyLibraryLocationEditing struct{}

func (readyLibraryLocationEditing) LibraryLocationEditingReady(context.Context) bool { return true }

// task3LocationsHandler composes the real ordinary and location handlers for
// one synthetic platform administrator. Filesystem-only work opens no
// provider; the browser only seals and opens host evidence.
func task3LocationsHandler(t *testing.T, pool *pgxpool.Pool, name string) (*BloemLibraryLocationsHandler, auth.AdminContextClaims, *recordingLibraryScanQueue) {
	t.Helper()
	actor := auth.AdminContextClaims{Scope: auth.AdminScopePlatform, SessionID: uuid.NewString(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES($1,$2,'synthetic','admin',true) RETURNING id,account_incarnation_id`, name, name+"@example.test").Scan(&actor.AccountID, &actor.AccountIncarnationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO auth_sessions(id,user_id,device_name,expires_at) VALUES($1,$2,'Synthetic Task3 fixture',now()+interval '1 hour')`, actor.SessionID, actor.AccountID); err != nil {
		t.Fatal(err)
	}
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-task3-paths", 3)))
	if err != nil {
		t.Fatal(err)
	}
	browser, err := nativestorage.NewSourceBrowser(pool, &nativestorage.Host{Registry: &plugins.NativeStorageRegistry{}, Manager: storageplugin.NewManager(storageplugin.Config{})}, cipher)
	if err != nil {
		t.Fatal(err)
	}
	locations := nativestorage.NewLibraryLocations(pool)
	locations.SetSourceBrowser(browser)
	queue := &recordingLibraryScanQueue{}
	libraries := NewLibraryHandler(catalog.NewFolderRepository(pool), nil, nil, pool, nil)
	libraries.ScanQueue = queue
	libraries.RealtimeMonitor = &countingMonitorPoker{}
	h := (&BloemLibraryLocationsHandler{Libraries: libraries, Locations: locations, Capabilities: readyLibraryLocationEditing{}}).ForAdminScope(auth.AdminScopePlatform)
	return h, actor, queue
}

// task3Serve calls one location handler as the signed administrative-context
// middleware would, with library_id set when it is positive.
func task3Serve(t *testing.T, actor auth.AdminContextClaims, libraryID int, method string, body any, call http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, "/", reader)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	route := chi.NewRouteContext()
	if libraryID > 0 {
		route.URLParams.Add("library_id", strconv.Itoa(libraryID))
	}
	ctx := context.WithValue(middleware.SetAdminContextClaims(r.Context(), actor), chi.RouteCtxKey, route)
	w := httptest.NewRecorder()
	call(w, r.WithContext(ctx))
	return w
}

// The replacement handler must keep Silo's postcommit path-change scan. Only a
// confirmed commit counts: conflicts, rollbacks and unknown outcomes queue none.
func TestLibraryLocationsReplaceQueuesPathChangeScanDB(t *testing.T) {
	pool := task3LibraryDatabase(t)
	h, actor, queue := task3LocationsHandler(t, pool, "task3-paths-admin")
	created, err := h.Libraries.CreateLibrary(t.Context(), LibraryCreateRequest{Name: "Paths", Type: "movies", Paths: []string{"/media/A"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := queue.take(); !reflect.DeepEqual(got, []string{strconv.Itoa(created.ID) + ":library_created"}) {
		t.Fatalf("creation follow-up: %v", got)
	}
	serve := func(method string, body any, call http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		return task3Serve(t, actor, created.ID, method, body, call)
	}
	read := func() nativestorage.LibrarySelection {
		t.Helper()
		w := serve(http.MethodGet, nil, h.HandleGet)
		var view nativestorage.LibrarySelection
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &view) != nil {
			t.Fatalf("read status=%d body=%s", w.Code, w.Body)
		}
		return view
	}
	replace := func(view nativestorage.LibrarySelection, paths ...string) *httptest.ResponseRecorder {
		t.Helper()
		return serve(http.MethodPut, nativestorage.ReplaceLocationsRequest{Paths: paths, Selections: []nativestorage.RootSelection{}, ExpectedRevision: view.Revision, ExpectedEditToken: view.EditToken}, h.HandleReplace)
	}
	first := read()
	w := replace(first, " /media/B/../B ")
	if w.Code != http.StatusOK {
		t.Fatalf("replace status=%d body=%s", w.Code, w.Body)
	}
	if got, want := queue.take(), []string{strconv.Itoa(created.ID) + ":library_paths_changed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("path change follow-up=%v want %v", got, want)
	}
	saved := read()
	if !reflect.DeepEqual(saved.Paths, []string{"/media/B"}) {
		t.Fatalf("saved paths: %v", saved.Paths)
	}
	// An unchanged path set saves without Silo's path-change rescan.
	if w := replace(saved, "/media/B"); w.Code != http.StatusOK {
		t.Fatalf("unchanged replace status=%d body=%s", w.Code, w.Body)
	}
	if got := queue.take(); len(got) != 0 {
		t.Fatalf("unchanged paths queued %v", got)
	}
	// A stale token is a conflict, not a save.
	assertNativeError(t, replace(first, "/media/stale"), http.StatusConflict, "revision_conflict")
	if got := queue.take(); len(got) != 0 {
		t.Fatalf("conflict queued %v", got)
	}
	if _, err := pool.Exec(t.Context(), `
CREATE FUNCTION task3_reject_path() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.path='/media/rollback' THEN RAISE EXCEPTION 'path rejected'; END IF; RETURN NEW; END $$;
CREATE TRIGGER task3_reject_path BEFORE INSERT ON media_folder_paths FOR EACH ROW EXECUTE FUNCTION task3_reject_path();
CREATE FUNCTION task3_reject_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.path='/media/unknown' THEN RAISE EXCEPTION 'commit rejected'; END IF; RETURN NULL; END $$;
CREATE CONSTRAINT TRIGGER task3_reject_commit AFTER INSERT ON media_folder_paths DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION task3_reject_commit();`); err != nil {
		t.Fatal(err)
	}
	current := read()
	// A statement failure rolls back before commit.
	if w := replace(current, "/media/rollback"); w.Code == http.StatusOK {
		t.Fatal("rolled-back replacement reported success")
	}
	// A commit failure is reported as an unknown outcome, never as a save.
	assertNativeError(t, replace(current, "/media/unknown"), http.StatusServiceUnavailable, "mutation_outcome_unknown")
	if got := queue.take(); len(got) != 0 {
		t.Fatalf("unconfirmed replacement queued %v", got)
	}
	if after := read(); after.Revision != current.Revision || !reflect.DeepEqual(after.Paths, current.Paths) {
		t.Fatalf("failed replacement changed library: %+v", after)
	}
}

// A filesystem path with a plugin source is refused from the request shape
// before selection preparation, so no provider is opened for it.
func TestLibraryLocationsCreateRefusesMixingBeforePreparationDB(t *testing.T) {
	pool := task3LibraryDatabase(t)
	h, actor, queue := task3LocationsHandler(t, pool, "task3-create-guard-admin")
	// Preparing this reference would fail differently; it is never opened.
	selections := []map[string]any{{"source_key": uuid.NewString(), "configuration_revision": 1, "selection_ref": "unprepared-reference"}}
	body := map[string]any{"library": map[string]any{"name": "Guard", "type": "ebooks", "paths": []string{"/media/guard"}}, "selections": selections}
	assertNativeError(t, task3Serve(t, actor, 0, http.MethodPost, body, h.HandleCreate), http.StatusUnprocessableEntity, "location_mixing_unavailable")
	var folders int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_folders`).Scan(&folders); err != nil || folders != 0 {
		t.Fatalf("refused creation persisted %d libraries: %v", folders, err)
	}
	if got := queue.take(); len(got) != 0 {
		t.Fatalf("refused creation queued %v", got)
	}
	// Control: without paths the same request reaches preparation and fails there.
	body["library"] = map[string]any{"name": "Guard", "type": "ebooks", "paths": []string{}}
	if w := task3Serve(t, actor, 0, http.MethodPost, body, h.HandleCreate); w.Code < 400 || strings.Contains(w.Body.String(), "location_mixing_unavailable") {
		t.Fatalf("plugin-only control status=%d body=%s", w.Code, w.Body)
	}
}

// UpdateLibrary's storage protections follow the retained location collection,
// not the scalar StorageSourceKey, which is nil for mixed and multi-source
// libraries once mixed locations are enabled.
func TestLibraryUpdateStorageGuardUsesLocationCollectionDB(t *testing.T) {
	pool := task3LibraryDatabase(t)
	var platform uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&platform); err != nil {
		t.Fatal(err)
	}
	var installation int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id,runtime_generation) VALUES('task3-guard','1','/synthetic',$1,1) RETURNING id`, platform).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, installation, platform); err != nil {
		t.Fatal(err)
	}
	sources := storagesource.NewRepository(pool)
	source := func(id string) uuid.UUID {
		t.Helper()
		s, err := sources.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: platform, InstallationID: &installation, PluginID: "task3-guard", ProviderSourceID: id, RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return s.Key
	}
	// These shapes are written directly: the mixing guard refuses them through
	// the location API until mixed ingestion, but UpdateLibrary must hold then.
	library := func(name string, paths []string, keys ...uuid.UUID) int {
		t.Helper()
		var id int
		if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name,owner_id) VALUES('ebooks',$1,$2) RETURNING id`, name, platform).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			if _, err := pool.Exec(t.Context(), `INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,$2)`, id, path); err != nil {
				t.Fatal(err)
			}
		}
		for _, key := range keys {
			if _, err := pool.Exec(t.Context(), `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,$3)`, uuid.New(), key, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	single := library("Single source", nil, source("single"))
	twoSources := library("Two sources", nil, source("first"), source("second"))
	mixed := library("Mixed", []string{"/media/mixed"}, source("mixed"))
	filesystem := library("Filesystem", []string{"/media/filesystem"})
	repo := catalog.NewFolderRepository(pool)
	for id, scalar := range map[int]bool{single: true, twoSources: false, mixed: false, filesystem: false} {
		f, err := repo.GetByID(t.Context(), id)
		if err != nil || (f.StorageSourceKey != nil) != scalar {
			t.Fatalf("library %d scalar projection: %+v %v", id, f, err)
		}
	}
	libraries := NewLibraryHandler(repo, nil, nil, pool, nil)
	queue := &recordingLibraryScanQueue{}
	libraries.ScanQueue = queue
	libraries.RealtimeMonitor = &countingMonitorPoker{}
	names := map[int]string{single: "single source", twoSources: "two sources", mixed: "mixed", filesystem: "filesystem"}
	refused := func(id int, field string, req LibraryUpdateRequest) {
		t.Helper()
		_, err := libraries.UpdateLibrary(t.Context(), id, 0, req)
		var api *APIError
		if !errors.As(err, &api) || api.Status != http.StatusBadRequest || api.Field != field {
			t.Fatalf("%s library: %v, want a %s refusal", names[id], err, field)
		}
	}
	tv, added := "tv", []string{"/media/added"}
	for _, id := range []int{single, twoSources, mixed} {
		refused(id, "type", LibraryUpdateRequest{Type: &tv})
	}
	for _, id := range []int{single, twoSources} {
		refused(id, "paths", LibraryUpdateRequest{Paths: &added})
	}
	// Other settings still save, and a mixed library keeps ordinary path edits,
	// which the location edit token then detects.
	for _, id := range []int{single, twoSources, mixed} {
		name := "Renamed " + strconv.Itoa(id)
		if _, err := libraries.UpdateLibrary(t.Context(), id, 0, LibraryUpdateRequest{Name: &name}); err != nil {
			t.Fatal(err)
		}
	}
	moved := []string{"/media/mixed-moved"}
	if _, err := libraries.UpdateLibrary(t.Context(), mixed, 0, LibraryUpdateRequest{Paths: &moved}); err != nil {
		t.Fatal(err)
	}
	if got, want := queue.take(), []string{strconv.Itoa(mixed) + ":library_paths_changed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed path follow-up=%v want %v", got, want)
	}
	// A filesystem library keeps Silo's ordinary type change.
	if _, err := libraries.UpdateLibrary(t.Context(), filesystem, 0, LibraryUpdateRequest{Type: &tv}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int]struct {
		kind      string
		paths     []string
		locations int
	}{single: {"ebooks", nil, 1}, twoSources: {"ebooks", nil, 2}, mixed: {"ebooks", moved, 1}, filesystem: {"tv", []string{"/media/filesystem"}, 0}} {
		var kind string
		var paths []string
		var locations int
		if err := pool.QueryRow(t.Context(), `SELECT f.type, COALESCE((SELECT array_agg(path ORDER BY id) FROM media_folder_paths WHERE media_folder_id=f.id),'{}'), (SELECT count(*) FROM library_storage_locations WHERE folder_id=f.id) FROM media_folders f WHERE f.id=$1`, id).Scan(&kind, &paths, &locations); err != nil {
			t.Fatal(err)
		}
		if kind != want.kind || len(paths) != len(want.paths) || len(paths) > 0 && !reflect.DeepEqual(paths, want.paths) || locations != want.locations {
			t.Fatalf("%s library: type=%s paths=%v locations=%d, want %+v", names[id], kind, paths, locations, want)
		}
	}
}

// task3StorageAttachment is a legacy storage attachment with a fixed outcome.
type task3StorageAttachment struct{ err error }

func (a task3StorageAttachment) AttachTx(context.Context, pgx.Tx, uuid.UUID, int) error {
	return a.err
}

// Legacy storage_source creation keeps its logged internal error for typed
// attachment failures. Only the explicit attachment returns them unchanged.
func TestLegacyStorageCreationKeepsLoggedInternalErrorDB(t *testing.T) {
	pool := task3LibraryDatabase(t)
	var logs bytes.Buffer
	var logsMu sync.Mutex
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{mu: &logsMu, w: &logs}, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	logged := func() string {
		logsMu.Lock()
		defer logsMu.Unlock()
		out := logs.String()
		logs.Reset()
		return out
	}
	// A deferred constraint fails the commit, so the creation outcome is unknown.
	if _, err := pool.Exec(t.Context(), `
CREATE FUNCTION task3_reject_library_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.name LIKE 'Unknown%' THEN RAISE EXCEPTION 'commit rejected'; END IF; RETURN NULL; END $$;
CREATE CONSTRAINT TRIGGER task3_reject_library_commit AFTER INSERT ON media_folders DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION task3_reject_library_commit();`); err != nil {
		t.Fatal(err)
	}
	libraries := NewLibraryHandler(catalog.NewFolderRepository(pool), nil, nil, pool, nil)
	libraries.ScanQueue = noopLibraryScanQueue{}
	libraries.RealtimeMonitor = &countingMonitorPoker{}
	key := uuid.New()
	for _, tc := range []struct {
		name   string
		attach error
		typed  func(error) bool
	}{
		{"Refused", &catalog.NativeOnboardingError{Code: "not_found"}, func(err error) bool {
			var native *catalog.NativeOnboardingError
			return errors.As(err, &native) && native.Code == "not_found"
		}},
		{"Unknown", nil, func(err error) bool {
			var unknown *catalog.MutationOutcomeUnknown
			return errors.As(err, &unknown) && unknown.LibraryID > 0 && unknown.Operation == "create_library"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			libraries.StorageLocations = task3StorageAttachment{err: tc.attach}
			_, err := libraries.CreateLibrary(t.Context(), LibraryCreateRequest{Name: tc.name + " legacy", Type: "ebooks", StorageSource: &key})
			var api *APIError
			if !errors.As(err, &api) || api.Status != http.StatusInternalServerError || api.Code != "internal_error" || api.Message != "Failed to create library" {
				t.Fatalf("legacy storage creation: %#v", err)
			}
			if out := logged(); !strings.Contains(out, "creating library") {
				t.Fatalf("legacy failure not logged: %q", out)
			}
			_, err = libraries.CreateLibraryWithAttachment(t.Context(), LibraryCreateRequest{Name: tc.name + " explicit", Type: "ebooks"}, func(context.Context, pgx.Tx, int) error { return tc.attach })
			if !tc.typed(err) {
				t.Fatalf("explicit attachment error: %#v", err)
			}
			// A domain refusal is the caller's answer; an unknown outcome is logged.
			if out := logged(); strings.Contains(out, "creating library") != (tc.attach == nil) {
				t.Fatalf("explicit attachment log: %q", out)
			}
		})
	}
	var persisted int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_folders`).Scan(&persisted); err != nil || persisted != 0 {
		t.Fatalf("failed creations persisted %d libraries: %v", persisted, err)
	}
}

// lockedWriter serializes log output written while the test reads it.
type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
