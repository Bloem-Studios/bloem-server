//go:build integration

package nativestorage

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func mutationFixture(t *testing.T) (*LibraryLocations, auth.AdminContextClaims, []RootSelection) {
	t.Helper()
	b, a, s, _ := newBrowseFixture(t)
	l := NewLibraryLocations(b.pool)
	l.SetSourceBrowser(b)
	// These cases exercise the mixed shapes that mixed ingestion will enable;
	// TestLibraryLocationsMixingGuardDB covers the guard that refuses them now.
	l.SetMixedLocationsReady(true)
	page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{})
	if err != nil {
		t.Fatal(err)
	}
	path, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.host.Registry.Install(t.Context(), plugins.NativeStorageInstallRequest{ArtifactKey: "s3", Binary: binary, Config: map[string]map[string]any{"connection": {"source_id": "second"}}, Source: storagesource.SourceConfig{OwnerID: s.OwnerID, ProviderSourceID: "second", RootEntryID: "root", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := b.runtime.(browseRuntime)
	runtime.description.Sources = append(runtime.description.Sources, &storagev1.Source{Id: "second", RootEntryId: "root", RevisionPinnedReads: true})
	secondPage, err := b.Browse(t.Context(), a, second.Source.Key, BrowseRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return l, a, []RootSelection{{SourceKey: s.Key, ConfigurationRevision: 1, SelectionRef: page.Directories[0].SelectionRef}, {SourceKey: second.Source.Key, ConfigurationRevision: 1, SelectionRef: secondPage.Directories[1].SelectionRef}}
}
func createMutationLibrary(t *testing.T, l *LibraryLocations, a auth.AdminContextClaims, roots []RootSelection, paths []string) int {
	t.Helper()
	p, err := l.PrepareCreation(t.Context(), a, nil, len(paths) > 0, roots)
	if err != nil {
		t.Fatal(err)
	}
	f, err := catalog.NewFolderRepository(l.pool).Create(t.Context(), catalog.CreateFolderInput{Name: "mixed", Type: "ebooks", Paths: paths, AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error { return l.AttachCreationTx(ctx, tx, a, id, p) }})
	if err != nil {
		t.Fatalf("create error: %#v cause: %#v", err, errors.Unwrap(err))
	}
	return f.ID
}
func requireLocationConflict(t *testing.T, err error, revision int64) {
	t.Helper()
	var conflict *catalog.NativeOnboardingError
	if !errors.As(err, &conflict) || conflict.Code != "revision_conflict" || conflict.CurrentLibraryRevision == nil || *conflict.CurrentLibraryRevision != revision {
		t.Fatalf("want reload conflict at %d: %v (%+v)", revision, err, conflict)
	}
}
func TestMixedLibraryCreationAtomicDB(t *testing.T) {
	l, a, roots := mutationFixture(t)
	id := createMutationLibrary(t, l, a, roots, []string{"/media/mixed"})
	view, err := l.ReadSelections(t.Context(), a, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Revision != 1 || view.EditToken == "" || len(view.Locations) != 2 || len(view.Selections) != 2 || !reflect.DeepEqual(view.Paths, []string{"/media/mixed"}) {
		t.Fatalf("mixed view: %+v", view)
	}
	f, err := catalog.NewFolderRepository(l.pool).GetByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if f.StorageSourceKey != nil {
		t.Fatal("mixed library has singular legacy source")
	}
}
func TestMixedLibraryCreationLastRootRollbackDB(t *testing.T) {
	l, a, roots := mutationFixture(t)
	if _, err := l.pool.Exec(t.Context(), `CREATE FUNCTION reject_last_root() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.directory_id='empty' THEN RAISE EXCEPTION 'last root rejected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_last_root BEFORE INSERT ON bloem_storage_selected_roots FOR EACH ROW EXECUTE FUNCTION reject_last_root()`); err != nil {
		t.Fatal(err)
	}
	p, err := l.PrepareCreation(t.Context(), a, nil, true, roots)
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.NewFolderRepository(l.pool).Create(t.Context(), catalog.CreateFolderInput{Name: "must roll back", Type: "ebooks", Paths: []string{"/media/rollback"}, AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error { return l.AttachCreationTx(ctx, tx, a, id, p) }})
	if err == nil {
		t.Fatal("last root failure admitted library")
	}
	for _, table := range []string{"media_folders", "media_folder_paths", "library_storage_locations", "bloem_storage_selected_roots", "bloem_library_location_state"} {
		var count int
		if err := l.pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rollback %s count=%d err=%v", table, count, err)
		}
	}
}

type mutationCommitCancelTracer struct {
	cancel context.CancelFunc
	commit atomic.Bool
}

func (tr *mutationCommitCancelTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	tr.commit.Store(data.SQL == "commit")
	return ctx
}
func (tr *mutationCommitCancelTracer) TraceQueryEnd(_ context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if tr.commit.Swap(false) && data.Err == nil {
		tr.cancel()
	}
}

func TestMixedLibraryCreationPostCommitReadRequiresReconciliationDB(t *testing.T) {
	l, a, roots := mutationFixture(t)
	p, err := l.PrepareCreation(t.Context(), a, nil, true, roots)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := l.pool.Config().Copy()
	cfg.MaxConns = 1
	cfg.ConnConfig.Tracer = &mutationCommitCancelTracer{cancel: cancel}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = catalog.NewFolderRepository(pool).Create(ctx, catalog.CreateFolderInput{Name: "committed", Type: "ebooks", Paths: []string{"/media/committed"}, AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error { return l.AttachCreationTx(ctx, tx, a, id, p) }})
	var unknown *catalog.MutationOutcomeUnknown
	if !errors.As(err, &unknown) || unknown.LibraryID <= 0 || unknown.Operation != "create_library" || unknown.OperationID == uuid.Nil {
		t.Fatalf("postcommit response lost recovery identifiers: %v", err)
	}
	view, err := l.ReadSelections(t.Context(), a, unknown.LibraryID)
	if err != nil || len(view.Locations) != 2 || len(view.Selections) != 2 || len(view.Paths) != 1 {
		t.Fatalf("committed creation cannot be reconciled: %+v %v", view, err)
	}
}
func TestLibraryLocationsLegacyPathCASDB(t *testing.T) {
	for _, mode := range []string{"replacement", "ABA", "empty", "same-rows-update"} {
		t.Run(mode, func(t *testing.T) {
			l, a, roots := mutationFixture(t)
			id := createMutationLibrary(t, l, a, roots, []string{"/media/A"})
			view, err := l.ReadSelections(t.Context(), a, id)
			if err != nil {
				t.Fatal(err)
			}
			repo := catalog.NewFolderRepository(l.pool)
			paths := []string{"/media/B"}
			if mode == "empty" {
				paths = []string{}
			}
			if mode == "same-rows-update" {
				_, err = l.pool.Exec(t.Context(), `UPDATE media_folder_paths SET path=path WHERE media_folder_id=$1`, id)
			} else {
				err = repo.Update(t.Context(), id, catalog.UpdateFolderInput{Paths: &paths})
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "ABA" {
				paths = []string{"/media/A"}
				if err := repo.Update(t.Context(), id, catalog.UpdateFolderInput{Paths: &paths}); err != nil {
					t.Fatal(err)
				}
			}
			_, err = l.ReplaceSelections(t.Context(), a, id, ReplaceLocationsRequest{Paths: []string{"/media/new"}, Selections: roots, ExpectedRevision: view.Revision, ExpectedEditToken: view.EditToken})
			requireLocationConflict(t, err, view.Revision)
			current, err := l.ReadSelections(t.Context(), a, id)
			if err != nil {
				t.Fatal(err)
			}
			if current.Revision != view.Revision || current.EditToken == view.EditToken {
				t.Fatal("legacy write did not invalidate edit token")
			}
		})
	}
}

// Path rows are the legacy-write evidence. From an empty path set, a legacy
// write that leaves a row is detected. An interval that ends empty again leaves
// no row to compare: the save then replaces exactly the empty set it read, so no
// committed path set is overwritten. Silo's v1/v2 updates never write [] paths.
func TestLibraryLocationsEmptyPathEvidenceScopeDB(t *testing.T) {
	for _, mode := range []string{"insert", "empty-ABA", "empty-to-empty"} {
		t.Run(mode, func(t *testing.T) {
			l, a, roots := mutationFixture(t)
			id := createMutationLibrary(t, l, a, roots[:1], []string{})
			view, err := l.ReadSelections(t.Context(), a, id)
			if err != nil {
				t.Fatal(err)
			}
			legacy := func(paths ...string) {
				t.Helper()
				replacement := append([]string{}, paths...)
				if err := catalog.NewFolderRepository(l.pool).Update(t.Context(), id, catalog.UpdateFolderInput{Paths: &replacement}); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "insert":
				legacy("/media/B")
			case "empty-ABA":
				legacy("/media/B")
				legacy()
			case "empty-to-empty":
				legacy()
			}
			saved, err := l.ReplaceSelections(t.Context(), a, id, ReplaceLocationsRequest{Paths: []string{}, Selections: []RootSelection{view.Selections[0].RootSelection}, ExpectedRevision: view.Revision, ExpectedEditToken: view.EditToken})
			if mode == "insert" {
				requireLocationConflict(t, err, view.Revision)
				return
			}
			if err != nil || len(saved.PreviousPaths) != 0 || len(saved.Paths) != 0 || saved.Revision != view.Revision+1 {
				t.Fatalf("empty interval: %+v %v", saved, err)
			}
		})
	}
}
func TestLibraryLocationsReplaceAtomicAndRetainsRootsDB(t *testing.T) {
	l, a, roots := mutationFixture(t)
	id := createMutationLibrary(t, l, a, roots, []string{"/media/A"})
	first, err := l.ReadSelections(t.Context(), a, id)
	if err != nil {
		t.Fatal(err)
	}
	next, err := l.ReplaceSelections(t.Context(), a, id, ReplaceLocationsRequest{Paths: []string{}, Selections: roots[:1], ExpectedRevision: first.Revision, ExpectedEditToken: first.EditToken})
	if err != nil {
		t.Fatal(err)
	}
	if next.Revision != 2 || len(next.Paths) != 0 || len(next.Selections) != 1 || len(next.Locations) != 2 {
		t.Fatalf("replace: %+v", next)
	}
	var disabled int
	if err := l.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_selected_roots WHERE NOT enabled`).Scan(&disabled); err != nil || disabled != 1 {
		t.Fatalf("retained removed root=%d %v", disabled, err)
	}
	f, err := catalog.NewFolderRepository(l.pool).GetByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if f.StorageSourceKey != nil {
		t.Fatal("multiple retained locations projected as one")
	}
	_, err = l.ReplaceSelections(t.Context(), a, id, ReplaceLocationsRequest{Paths: []string{"/media/stale"}, Selections: roots, ExpectedRevision: first.Revision, ExpectedEditToken: first.EditToken})
	requireLocationConflict(t, err, 2)
	// Fresh references from a saved selection permit replacing without browsing.
	saved := next.Selections[0]
	_, err = l.ReplaceSelections(t.Context(), a, id, ReplaceLocationsRequest{Paths: []string{"/media/restored"}, Selections: []RootSelection{saved.RootSelection}, ExpectedRevision: next.Revision, ExpectedEditToken: next.EditToken})
	if err != nil {
		t.Fatal(err)
	}
}
func TestLibraryLocationsCrossedLegacyPathLockDB(t *testing.T) {
	l, a, roots := mutationFixture(t)
	id := createMutationLibrary(t, l, a, roots, []string{"/media/A"})
	view, err := l.ReadSelections(t.Context(), a, id)
	if err != nil {
		t.Fatal(err)
	}
	cfg := l.pool.Config().Copy()
	cfg.MaxConns = 2
	other, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	legacy, err := other.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = legacy.Rollback(context.Background()) }()
	// DELETE owns the path row before its subsequent INSERT needs parent KEY SHARE.
	if _, err := legacy.Exec(t.Context(), `DELETE FROM media_folder_paths WHERE media_folder_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	// First verify the public mutation returns the current revision promptly.
	_, err = l.ReplaceSelections(ctx, a, id, ReplaceLocationsRequest{Paths: []string{"/media/editor"}, Selections: roots, ExpectedRevision: view.Revision, ExpectedEditToken: view.EditToken})
	requireLocationConflict(t, err, 1)
	prepared, err := l.PrepareSelections(ctx, a, roots)
	if err != nil {
		t.Fatal(err)
	}
	edit, err := l.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = edit.Rollback(context.Background()) }()
	if _, err := l.RetainPreparedTx(ctx, edit, a, id, prepared); err != nil {
		t.Fatal(err)
	}
	inserted := make(chan error, 1)
	go func() {
		_, err := legacy.Exec(ctx, `INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,'/media/legacy')`, id)
		inserted <- err
	}()
	// Observe the legacy INSERT waiting on the retained parent FK; no sleeps.
	for {
		var waiting bool
		if err := edit.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND NOT granted)`, legacy.Conn().PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		runtime.Gosched()
	}
	_, err = libraryPathsTx(ctx, edit, id)
	if !pathLockUnavailable(err) {
		t.Fatalf("crossed path lock should return NOWAIT: %v", err)
	}
	if err := edit.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-inserted:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("legacy INSERT deadlocked")
	}
	if err := legacy.Commit(ctx); err != nil {
		t.Fatal(err)
	}

}
func TestLibraryLocationsSharedSourceAndHiddenLibraryDB(t *testing.T) {
	l, a, roots := mutationFixture(t)
	org, membership, policy, security := discoveryOrganization(t, l.pool, a.AccountID)
	a.Scope = auth.AdminScopeOrganization
	a.OrganizationID = org
	a.MembershipID = membership
	a.PolicyRevision = policy
	a.SecurityRevision = security
	a.EffectiveAuthority = "organization_admin"
	if _, err := l.pool.Exec(t.Context(), `INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) SELECT $1,'plugin_availability','plugin_installation',owner_id,installation_id,'active','mutation-test' FROM bloem_storage_sources`, org); err != nil {
		t.Fatal(err)
	}
	// References are actor-bound; obtain organization references.
	for i := range roots {
		page, err := l.browser.Browse(t.Context(), a, roots[i].SourceKey, BrowseRequest{})
		if err != nil {
			t.Fatal(err)
		}
		roots[i].SelectionRef = page.Current.SelectionRef
	}
	id := createMutationLibrary(t, l, a, roots, []string{"/media/org"})
	var ownerOrg uuid.UUID
	if err := l.pool.QueryRow(t.Context(), `SELECT o.organization_id FROM media_folders f JOIN resource_owners o ON o.id=f.owner_id WHERE f.id=$1`, id).Scan(&ownerOrg); err != nil || ownerOrg != org {
		t.Fatalf("wrong creation owner %s %v", ownerOrg, err)
	}
	// Another organization's library remains hidden even with a valid source.
	platform := a
	platform.Scope = auth.AdminScopePlatform
	platform.OrganizationID = uuid.Nil
	platform.MembershipID = uuid.Nil
	platform.PolicyRevision = 0
	platform.SecurityRevision = 0
	platform.EffectiveAuthority = ""
	hidden, err := catalog.NewFolderRepository(l.pool).Create(t.Context(), catalog.CreateFolderInput{Name: "platform", Type: "ebooks", Paths: []string{"/media/platform"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.ReadSelections(t.Context(), a, hidden.ID)
	var typed *catalog.NativeOnboardingError
	if !errors.As(err, &typed) || typed.Code != "not_found" {
		t.Fatalf("hidden library: %v", err)
	}
	_, err = l.ReadSelections(t.Context(), platform, id)
	if err != nil {
		t.Fatal("platform admin should see organization library", err)
	}
}
func TestLibraryLocationsUnavailableRemovalDB(t *testing.T) {
	l, a, roots := mutationFixture(t)
	id := createMutationLibrary(t, l, a, roots, []string{"/media/A"})
	if _, err := l.pool.Exec(t.Context(), `UPDATE bloem_storage_sources SET enabled=false WHERE key=$1`, roots[1].SourceKey); err != nil {
		t.Fatal(err)
	}
	view, err := l.ReadSelections(t.Context(), a, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.ReplaceSelections(t.Context(), a, id, ReplaceLocationsRequest{Paths: []string{"/media/A"}, Selections: roots[:1], ExpectedRevision: view.Revision, ExpectedEditToken: view.EditToken})
	if err != nil {
		t.Fatal("removed unavailable source required provider", err)
	}
}

func TestLibraryLocationsEditRollbackDB(t *testing.T) {
	l, a, roots := mutationFixture(t)
	id := createMutationLibrary(t, l, a, roots[:1], []string{"/media/A"})
	before, err := l.ReadSelections(t.Context(), a, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.pool.Exec(t.Context(), `CREATE FUNCTION reject_edit_root() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.directory_id='empty' THEN RAISE EXCEPTION 'last root rejected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_edit_root BEFORE INSERT ON bloem_storage_selected_roots FOR EACH ROW EXECUTE FUNCTION reject_edit_root()`); err != nil {
		t.Fatal(err)
	}
	_, err = l.ReplaceSelections(t.Context(), a, id, ReplaceLocationsRequest{Paths: []string{"/media/B"}, Selections: roots, ExpectedRevision: before.Revision, ExpectedEditToken: before.EditToken})
	if err == nil {
		t.Fatal("failed final root committed edit")
	}
	after, err := l.ReadSelections(t.Context(), a, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || !reflect.DeepEqual(after.Paths, before.Paths) || len(after.Locations) != 1 || len(after.Selections) != 1 {
		t.Fatalf("partial edit committed: %+v", after)
	}
}

func TestLibraryLocationsEditTokenActorAndExpiryDB(t *testing.T) {
	for _, mode := range []string{"actor", "tamper", "expiry", "other-library"} {
		t.Run(mode, func(t *testing.T) {
			l, a, roots := mutationFixture(t)
			id := createMutationLibrary(t, l, a, roots, []string{"/media/A"})
			before, err := l.ReadSelections(t.Context(), a, id)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "actor" {
				original := a
				a.ExpiresAt = a.ExpiresAt.Add(-time.Minute)
				for i := range roots {
					page, err := l.browser.Browse(t.Context(), a, roots[i].SourceKey, BrowseRequest{})
					if err != nil {
						t.Fatal(err)
					}
					roots[i].SelectionRef = page.Current.SelectionRef
				}
				if original.ExpiresAt.Equal(a.ExpiresAt) {
					t.Fatal("actor not changed")
				}
			}
			if mode == "tamper" {
				before.EditToken = "modified" + before.EditToken
			}
			if mode == "expiry" {
				l.browser.now = func() time.Time { return time.Now().Add(maxBrowseLifetime + time.Minute) }
				p, err := l.PrepareSelections(t.Context(), a, []RootSelection{})
				if err != nil || p == nil {
					t.Fatal(err)
				}
				roots = []RootSelection{}
			}
			if mode == "other-library" {
				other := createMutationLibrary(t, l, a, roots, []string{"/media/other"})
				view, err := l.ReadSelections(t.Context(), a, other)
				if err != nil {
					t.Fatal(err)
				}
				before.EditToken = view.EditToken
			}
			_, err = l.ReplaceSelections(t.Context(), a, id, ReplaceLocationsRequest{Paths: []string{"/media/replacement"}, Selections: roots, ExpectedRevision: before.Revision, ExpectedEditToken: before.EditToken})
			requireLocationConflict(t, err, 1)
		})
	}
}

func requireNativeCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *catalog.NativeOnboardingError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("want %s: %v", code, err)
	}
}

// Silo's library type is free text classified by librarykind. Ebook aliases
// keep their native selections on creation and when an existing library is edited.
func TestLibraryLocationsAliasLibraryTypesDB(t *testing.T) {
	for _, libraryType := range []string{"ebook", "Ebook", "  EBOOKS  "} {
		t.Run("create/"+strings.TrimSpace(libraryType), func(t *testing.T) {
			l, a, roots := mutationFixture(t)
			p, err := l.PrepareCreation(t.Context(), a, nil, false, roots[:1])
			if err != nil {
				t.Fatal(err)
			}
			f, err := catalog.NewFolderRepository(l.pool).Create(t.Context(), catalog.CreateFolderInput{Name: "alias", Type: libraryType, Paths: []string{}, AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error { return l.AttachCreationTx(ctx, tx, a, id, p) }})
			if err != nil {
				t.Fatalf("alias-typed creation refused: %v", err)
			}
			view, err := l.ReadSelections(t.Context(), a, f.ID)
			if err != nil || len(view.Selections) != 1 {
				t.Fatalf("alias-typed selections: %+v %v", view, err)
			}
		})
		t.Run("edit/"+strings.TrimSpace(libraryType), func(t *testing.T) {
			l, a, roots := mutationFixture(t)
			id := createMutationLibrary(t, l, a, roots[:1], []string{})
			// An existing library carries the alias Silo accepted when it was saved.
			if _, err := l.pool.Exec(t.Context(), `UPDATE media_folders SET type=$2 WHERE id=$1`, id, libraryType); err != nil {
				t.Fatal(err)
			}
			view, err := l.ReadSelections(t.Context(), a, id)
			if err != nil {
				t.Fatal(err)
			}
			next, err := l.ReplaceSelections(t.Context(), a, id, ReplaceLocationsRequest{Paths: []string{}, Selections: []RootSelection{view.Selections[0].RootSelection}, ExpectedRevision: view.Revision, ExpectedEditToken: view.EditToken})
			if err != nil {
				t.Fatalf("alias-typed edit refused: %v", err)
			}
			if next.Revision != view.Revision+1 || len(next.Selections) != 1 {
				t.Fatalf("alias-typed edit: %+v", next)
			}
		})
	}
	t.Run("movie alias uses declarations", func(t *testing.T) {
		l, a, roots := mutationFixture(t)
		l.SetNativeMovieReady(true)
		p, err := l.PrepareCreation(t.Context(), a, nil, false, roots[:1])
		if err != nil {
			t.Fatal(err)
		}
		// Classified as movies; this provider declares ebooks only.
		_, err = catalog.NewFolderRepository(l.pool).Create(t.Context(), catalog.CreateFolderInput{Name: "movie alias", Type: " Movie ", Paths: []string{}, AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error { return l.AttachCreationTx(ctx, tx, a, id, p) }})
		requireNativeCode(t, err, "invalid_request")
	})
	t.Run("series stays unavailable", func(t *testing.T) {
		l, a, roots := mutationFixture(t)
		p, err := l.PrepareCreation(t.Context(), a, nil, false, roots[:1])
		if err != nil {
			t.Fatal(err)
		}
		_, err = catalog.NewFolderRepository(l.pool).Create(t.Context(), catalog.CreateFolderInput{Name: "series", Type: "tv", Paths: []string{}, AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error { return l.AttachCreationTx(ctx, tx, a, id, p) }})
		if err == nil {
			t.Fatal("series library admitted plugin selections")
		}
	})
}

// Until mixed-location scanning is composed, a library holds filesystem paths
// or one plugin source over its lifetime; retained locations count.
func TestLibraryLocationsMixingGuardDB(t *testing.T) {
	l, a, roots := mutationFixture(t)
	l.SetMixedLocationsReady(false)
	repo := catalog.NewFolderRepository(l.pool)
	create := func(paths []string, selected []RootSelection) (int, error) {
		p, err := l.PrepareCreation(t.Context(), a, nil, len(paths) > 0, selected)
		if err != nil {
			return 0, err
		}
		f, err := repo.Create(t.Context(), catalog.CreateFolderInput{Name: "guard", Type: "ebooks", Paths: paths, AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error { return l.AttachCreationTx(ctx, tx, a, id, p) }})
		if err != nil {
			return 0, err
		}
		return f.ID, nil
	}
	const unavailable = "location_mixing_unavailable"
	_, err := create([]string{"/media/guard"}, roots[:1])
	requireNativeCode(t, err, unavailable)
	_, err = create([]string{}, roots)
	requireNativeCode(t, err, unavailable)
	for _, table := range []string{"media_folders", "media_folder_paths", "library_storage_locations", "bloem_storage_selected_roots"} {
		var count int
		if err := l.pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("refused shape persisted %s=%d %v", table, count, err)
		}
	}
	// Several folders from one plugin source remain supported.
	page, err := l.browser.Browse(t.Context(), a, roots[0].SourceKey, BrowseRequest{})
	if err != nil || len(page.Directories) < 2 {
		t.Fatalf("second root unavailable: %+v %v", page, err)
	}
	sameSource := []RootSelection{roots[0], {SourceKey: roots[0].SourceKey, ConfigurationRevision: roots[0].ConfigurationRevision, SelectionRef: page.Directories[1].SelectionRef}}
	plugin, err := create([]string{}, sameSource)
	if err != nil {
		t.Fatal(err)
	}
	view, err := l.ReadSelections(t.Context(), a, plugin)
	if err != nil || len(view.Selections) != 2 || len(view.Locations) != 1 {
		t.Fatalf("one-source view: %+v %v", view, err)
	}
	kept := []RootSelection{view.Selections[0].RootSelection, view.Selections[1].RootSelection}
	for name, req := range map[string]ReplaceLocationsRequest{
		"add filesystem paths":      {Paths: []string{"/media/added"}, Selections: kept},
		"replace source with paths": {Paths: []string{"/media/only"}, Selections: []RootSelection{}},
		"switch to another source":  {Paths: []string{}, Selections: roots[1:]},
		"add a second source":       {Paths: []string{}, Selections: append([]RootSelection{kept[0]}, roots[1])},
	} {
		req.ExpectedRevision, req.ExpectedEditToken = view.Revision, view.EditToken
		_, err := l.ReplaceSelections(t.Context(), a, plugin, req)
		if err == nil {
			t.Fatalf("%s: unscannable shape saved", name)
		}
		requireNativeCode(t, err, unavailable)
	}
	after, err := l.ReadSelections(t.Context(), a, plugin)
	if err != nil || after.Revision != view.Revision || len(after.Locations) != 1 || len(after.Paths) != 0 {
		t.Fatalf("refused edit changed library: %+v %v", after, err)
	}
	if _, err := l.ReplaceSelections(t.Context(), a, plugin, ReplaceLocationsRequest{Paths: []string{}, Selections: kept[:1], ExpectedRevision: after.Revision, ExpectedEditToken: after.EditToken}); err != nil {
		t.Fatalf("same-source root edit refused: %v", err)
	}
	// Converting a filesystem library would leave Silo's local files without cleanup.
	local, err := repo.Create(t.Context(), catalog.CreateFolderInput{Name: "local", Type: "ebooks", Paths: []string{"/media/local"}})
	if err != nil {
		t.Fatal(err)
	}
	localView, err := l.ReadSelections(t.Context(), a, local.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.ReplaceSelections(t.Context(), a, local.ID, ReplaceLocationsRequest{Paths: []string{}, Selections: roots[:1], ExpectedRevision: localView.Revision, ExpectedEditToken: localView.EditToken})
	requireNativeCode(t, err, unavailable)
	moved, err := l.ReplaceSelections(t.Context(), a, local.ID, ReplaceLocationsRequest{Paths: []string{"/media/moved"}, Selections: []RootSelection{}, ExpectedRevision: localView.Revision, ExpectedEditToken: localView.EditToken})
	if err != nil {
		t.Fatalf("filesystem edit refused: %v", err)
	}
	if !reflect.DeepEqual(moved.PreviousPaths, []string{"/media/local"}) || !reflect.DeepEqual(moved.Paths, []string{"/media/moved"}) {
		t.Fatalf("replacement paths: previous=%v current=%v", moved.PreviousPaths, moved.Paths)
	}
	// Mixed ingestion flips the one switch; the same shape is then saved.
	l.SetMixedLocationsReady(true)
	mixed, err := l.ReplaceSelections(t.Context(), a, local.ID, ReplaceLocationsRequest{Paths: []string{"/media/moved"}, Selections: roots[:1], ExpectedRevision: moved.Revision, ExpectedEditToken: moved.EditToken})
	if err != nil || len(mixed.Locations) != 1 || len(mixed.Paths) != 1 {
		t.Fatalf("mixing ready refused mixed shape: %+v %v", mixed, err)
	}
}

// The request's filesystem paths count before any provider I/O: a refused
// paths-plus-source creation never opens the provider.
func TestLibraryLocationsPrepareCreationRefusesMixingBeforeProviderIODB(t *testing.T) {
	l, a, roots := mutationFixture(t)
	l.SetMixedLocationsReady(false)
	provider := l.browser.runtime.(browseRuntime).provider
	opened, release := make(chan struct{}), make(chan struct{})
	close(release)
	provider.mu.Lock()
	provider.holdStat, provider.started, provider.release = true, opened, release
	provider.mu.Unlock()
	_, err := l.PrepareCreation(t.Context(), a, nil, true, roots[:1])
	requireNativeCode(t, err, "location_mixing_unavailable")
	select {
	case <-opened:
		t.Fatal("refused shape opened the provider")
	default:
	}
	// Control: without paths the same selection is prepared through the provider.
	if _, err := l.PrepareCreation(t.Context(), a, nil, false, roots[:1]); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opened:
	default:
		t.Fatal("preparation never reached the provider, so the observation proves nothing")
	}
}
