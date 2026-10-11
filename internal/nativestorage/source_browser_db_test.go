//go:build integration

package nativestorage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"google.golang.org/grpc"
)

// Only the remote boundary is replaced: registry, actor checks, transactions,
// token cryptography and selection preparation are the production components.
type browseProvider struct {
	storagev1.StorageProviderClient
	mu       sync.Mutex
	entries  map[string]*storagev1.Entry
	pages    map[string]*storagev1.ListResponse
	started  chan struct{}
	release  <-chan struct{}
	holdStat bool
}

func browseEntry(id string) *storagev1.Entry {
	return &storagev1.Entry{Id: id, Name: id, LogicalPath: "display/" + id, Revision: "directory-v1", Kind: storagev1.EntryKind_ENTRY_KIND_DIRECTORY}
}
func (p *browseProvider) List(ctx context.Context, req *storagev1.ListRequest, _ ...grpc.CallOption) (*storagev1.ListResponse, error) {
	if !req.DirectoriesOnly || req.MaxEntries < 1 || req.MaxEntries > 100 || req.ChangesSince != "" {
		return nil, errors.New("browse must request bounded directory-only full listing")
	}
	p.mu.Lock()
	started, release := p.started, p.release
	p.started = nil
	page := p.pages[req.DirectoryId+"|"+req.Cursor]
	p.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if page == nil {
		return &storagev1.ListResponse{Complete: true}, nil
	}
	return page, nil
}
func (p *browseProvider) Stat(ctx context.Context, req *storagev1.StatRequest, _ ...grpc.CallOption) (*storagev1.StatResponse, error) {
	p.mu.Lock()
	entry := p.entries[req.EntryId]
	started, release := p.started, p.release
	if p.holdStat {
		p.started = nil
	}
	hold := p.holdStat
	p.mu.Unlock()
	if hold {
		if started != nil {
			close(started)
		}
		if release != nil {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	if entry == nil {
		return nil, errors.New("entry unavailable")
	}
	return &storagev1.StatResponse{Entry: entry}, nil
}

type browseRuntime struct {
	provider    *browseProvider
	description *storagev1.DescribeResponse
}

func (r browseRuntime) Borrow(context.Context, storageplugin.Snapshot) (Lease, error) {
	return Lease{Context: context.Background(), Provider: r.provider, Description: r.description}, nil
}
func (browseRuntime) Disable(int) {}
func newBrowseFixture(t *testing.T) (*SourceBrowser, auth.AdminContextClaims, storagesource.SourceConfig, *browseProvider) {
	t.Helper()
	path, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	management, actor, cmd := discoveryFixture(t, binary, "unused", true)
	var owner uuid.UUID
	if err := management.pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	snapshot, err := management.registry.Install(t.Context(), plugins.NativeStorageInstallRequest{ArtifactKey: cmd.ArtifactKey, Binary: binary, Config: cmd.Config, Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: cmd.ProviderSourceID, RootEntryID: "root", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-browse-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	browser, err := NewSourceBrowser(management.pool, management.installDiscoveryHost, cipher)
	if err != nil {
		t.Fatal(err)
	}
	provider := &browseProvider{entries: map[string]*storagev1.Entry{"root": browseEntry("root"), "nested": browseEntry("nested"), "empty": browseEntry("empty")}, pages: map[string]*storagev1.ListResponse{"root|": {Entries: []*storagev1.Entry{browseEntry("nested"), browseEntry("empty")}, Complete: true}}}
	browser.runtime = browseRuntime{provider: provider, description: &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: snapshot.Source.ProviderSourceID, RootEntryId: "root", RevisionPinnedReads: true}}}}
	browser.SetCursorStore(newBrowseMemoryStore())
	return browser, actor, snapshot.Source, provider
}
func TestSourceBrowserDirectoriesAndPreparedSelectionsDB(t *testing.T) {
	b, a, s, _ := newBrowseFixture(t)
	page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Current.Name != "root" || len(page.Directories) != 1 || page.Directories[0].Name != "nested" || page.NextCursor == "" {
		t.Fatalf("root page: %+v", page)
	}
	second, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{DirectoryRef: page.Current.SelectionRef, Cursor: page.NextCursor, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Directories) != 1 || second.Directories[0].Name != "empty" || second.NextCursor != "" {
		t.Fatalf("second page: %+v", second)
	}
	empty, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{DirectoryRef: second.Directories[0].SelectionRef})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Current.Name != "empty" || len(empty.Directories) != 0 || len(empty.Breadcrumbs) != 1 || empty.Breadcrumbs[0].Name != "root" {
		t.Fatalf("empty directory: %+v", empty)
	}
	l := NewLibraryLocations(b.pool)
	l.SetSourceBrowser(b)
	prepared, err := l.PrepareSelections(t.Context(), a, []RootSelection{{SourceKey: s.Key, ConfigurationRevision: 1, SelectionRef: empty.Current.SelectionRef}})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.roots) != 1 || prepared.roots[0].directoryID != "empty" {
		t.Fatal("empty directory was not retained")
	}
	_, err = l.PrepareSelections(t.Context(), a, []RootSelection{{SourceKey: s.Key, ConfigurationRevision: 1, SelectionRef: empty.Current.SelectionRef}, {SourceKey: s.Key, ConfigurationRevision: 1, SelectionRef: empty.Current.SelectionRef}})
	if err == nil {
		t.Fatal("duplicate selection accepted")
	}
	var scans int
	if err := b.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_scan_runs`).Scan(&scans); err != nil {
		t.Fatal(err)
	}
	if scans != 0 {
		t.Fatal("browse queued discovery")
	}
}
func TestSourceBrowserRejectsInvalidPagesDB(t *testing.T) {
	for _, mode := range []string{"repeat", "cycle", "duplicate", "malformed", "malformed-metadata", "file-directory", "too-many", "oversized", "directory-contract"} {
		t.Run(mode, func(t *testing.T) {
			b, a, s, p := newBrowseFixture(t)
			switch mode {
			case "repeat":
				p.pages["root|"] = &storagev1.ListResponse{NextCursor: "next"}
				p.pages["root|next"] = &storagev1.ListResponse{NextCursor: "next"}
			case "cycle":
				p.pages["root|"] = &storagev1.ListResponse{NextCursor: "a"}
				p.pages["root|a"] = &storagev1.ListResponse{NextCursor: "b"}
				p.pages["root|b"] = &storagev1.ListResponse{NextCursor: "a"}
			case "duplicate":
				p.pages["root|"] = &storagev1.ListResponse{Entries: []*storagev1.Entry{browseEntry("nested"), browseEntry("nested")}, Complete: true}
			case "malformed":
				e := browseEntry("bad")
				e.Name = string([]byte{0xff})
				p.pages["root|"] = &storagev1.ListResponse{Entries: []*storagev1.Entry{e}, Complete: true}
			case "malformed-metadata":
				e := browseEntry("bad")
				e.Ebook = &storagev1.EbookMetadata{Title: string([]byte{0xff})}
				p.pages["root|"] = &storagev1.ListResponse{Entries: []*storagev1.Entry{e}, Complete: true}
			case "file-directory":
				p.entries["root"].Kind = storagev1.EntryKind_ENTRY_KIND_FILE
			case "too-many":
				es := make([]*storagev1.Entry, 513)
				for i := range es {
					es[i] = browseEntry(uuid.NewString())
				}
				p.pages["root|"] = &storagev1.ListResponse{Entries: es, Complete: true}
			case "oversized":
				e := browseEntry("bad")
				e.LogicalPath = strings.Repeat("x", (1<<20)+1)
				p.pages["root|"] = &storagev1.ListResponse{Entries: []*storagev1.Entry{e}, Complete: true}
			case "directory-contract":
				r := b.runtime.(browseRuntime)
				r.description.Sources[0].DirectoryOnlyListing = true
				e := browseEntry("file")
				e.Kind = storagev1.EntryKind_ENTRY_KIND_FILE
				p.pages["root|"] = &storagev1.ListResponse{Entries: []*storagev1.Entry{e}, Complete: true}
			}
			if _, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{}); err == nil {
				t.Fatal("invalid provider response accepted")
			}
		})
	}
}
func TestSourceBrowserReferenceIsolationDB(t *testing.T) {
	for _, mode := range []string{"source", "account", "session", "organization", "tamper", "expired", "configuration", "generation", "root", "page-size", "ancestry"} {
		t.Run(mode, func(t *testing.T) {
			b, a, s, _ := newBrowseFixture(t)
			page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			req := BrowseRequest{DirectoryRef: page.Current.SelectionRef, Cursor: page.NextCursor, Limit: 1}
			key := s.Key
			switch mode {
			case "source":
				key = uuid.New()
			case "account":
				a.AccountID++
			case "session":
				a.SessionID = uuid.NewString()
			case "organization":
				a.Scope = auth.AdminScopeOrganization
				a.OrganizationID = uuid.New()
			case "tamper":
				req.DirectoryRef = req.DirectoryRef[:len(req.DirectoryRef)-5] + "xxxxx"
			case "expired":
				b.now = func() time.Time { return time.Now().Add(time.Hour) }
			case "configuration":
				_, err = b.pool.Exec(t.Context(), `UPDATE bloem_storage_sources SET configuration_revision=configuration_revision+1 WHERE key=$1`, s.Key)
			case "generation":
				_, err = b.pool.Exec(t.Context(), `UPDATE plugin_installations SET runtime_generation=runtime_generation+1 WHERE id=$1`, s.InstallationID)
			case "root":
				_, err = b.pool.Exec(t.Context(), `UPDATE bloem_storage_sources SET root_entry_id='elsewhere' WHERE key=$1`, s.Key)
			case "page-size":
				req.Limit = 2
			case "ancestry":
				req.DirectoryRef = page.Directories[0].SelectionRef
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Browse(t.Context(), a, key, req); err == nil {
				t.Fatal("isolated or stale token accepted")
			}
		})
	}
}
func TestSourceBrowserRechecksHeldProviderIODB(t *testing.T) {
	for _, phase := range []string{"List", "Stat"} {
		for _, effect := range []string{"disable", "uninstall", "session", "configuration"} {
			t.Run(phase+"/"+effect, func(t *testing.T) {
				b, a, s, p := newBrowseFixture(t)
				page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{})
				if err != nil {
					t.Fatal(err)
				}
				started, release := make(chan struct{}), make(chan struct{})
				p.mu.Lock()
				p.started = started
				p.release = release
				p.holdStat = phase == "Stat"
				p.mu.Unlock()
				result := make(chan error, 1)
				go func() {
					if phase == "Stat" {
						l := NewLibraryLocations(b.pool)
						l.SetSourceBrowser(b)
						_, err := l.PrepareSelections(t.Context(), a, []RootSelection{{SourceKey: s.Key, ConfigurationRevision: 1, SelectionRef: page.Current.SelectionRef}})
						result <- err
					} else {
						_, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{})
						result <- err
					}
				}()
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				select {
				case <-started:
				case err := <-result:
					t.Fatalf("provider did not start: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				// This pool has one connection; an acquisition here proves I/O holds none.
				if _, err := b.pool.Exec(ctx, `SELECT 1`); err != nil {
					close(release)
					t.Fatal("provider I/O retained pool", err)
				}
				switch effect {
				case "disable":
					_, err = b.pool.Exec(ctx, `UPDATE plugin_installations SET enabled=false WHERE id=$1`, s.InstallationID)
				case "uninstall":
					_, err = b.pool.Exec(ctx, `UPDATE bloem_storage_sources SET enabled=false,installation_id=NULL,configuration_revision=configuration_revision+1 WHERE key=$1`, s.Key)
				case "session":
					_, err = b.pool.Exec(ctx, `UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE id=$1`, a.SessionID)
				case "configuration":
					_, err = b.pool.Exec(ctx, `UPDATE bloem_storage_sources SET configuration_revision=configuration_revision+1 WHERE key=$1`, s.Key)
				}
				close(release)
				if err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-result:
					if err == nil {
						t.Fatal("authority revoked during I/O accepted")
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			})
		}
	}
}
func TestSourceBrowserSharedEntitlementRevocationDB(t *testing.T) {
	b, a, s, p := newBrowseFixture(t)
	org, membership, policy, security := discoveryOrganization(t, b.pool, a.AccountID)
	a.Scope = auth.AdminScopeOrganization
	a.OrganizationID = org
	a.MembershipID = membership
	a.PolicyRevision = policy
	a.SecurityRevision = security
	a.EffectiveAuthority = "organization_admin"
	var grant uuid.UUID
	if err := b.pool.QueryRow(t.Context(), `INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) VALUES($1,'plugin_availability','plugin_installation',$2,$3,'active','browse-test') RETURNING id`, org, s.OwnerID, s.InstallationID).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{}); err != nil {
		t.Fatal("shared entitled browse", err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	p.started = started
	p.release = release
	result := make(chan error, 1)
	go func() { _, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{}); result <- err }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("List did not start")
	}
	if _, err := b.pool.Exec(t.Context(), `UPDATE organization_entitlements SET status='revoked',revoked_at=clock_timestamp() WHERE id=$1`, grant); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("revoked entitlement accepted")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("List did not finish")
	}
}
func TestSourceBrowserPreparationLimitsDB(t *testing.T) {
	b, a, s, _ := newBrowseFixture(t)
	l := NewLibraryLocations(b.pool)
	l.SetSourceBrowser(b)
	roots := make([]RootSelection, 129)
	for i := range roots {
		roots[i] = RootSelection{SourceKey: s.Key, ConfigurationRevision: 1, SelectionRef: "invalid"}
	}
	_, err := l.PrepareSelections(t.Context(), a, roots)
	var typed *catalog.NativeOnboardingError
	if !errors.As(err, &typed) || typed.Code != "selection_limit_exceeded" {
		t.Fatalf("root bound: %v", err)
	}
	roots = make([]RootSelection, 17)
	for i := range roots {
		roots[i] = RootSelection{SourceKey: uuid.New(), ConfigurationRevision: 1, SelectionRef: "invalid"}
	}
	_, err = l.PrepareSelections(t.Context(), a, roots)
	if !errors.As(err, &typed) || typed.Code != "selection_limit_exceeded" {
		t.Fatalf("source bound: %v", err)
	}
}

func TestSourceBrowserFiltersLegacyFilesAndBoundsHostPagesDB(t *testing.T) {
	b, a, s, p := newBrowseFixture(t)
	entries := make([]*storagev1.Entry, 0, 512)
	for i := 0; i < 300; i++ {
		e := browseEntry("file-" + uuid.NewString())
		e.Kind = storagev1.EntryKind_ENTRY_KIND_FILE
		entries = append(entries, e)
	}
	for i := 0; i < 212; i++ {
		entries = append(entries, browseEntry("directory-"+uuid.NewString()))
	}
	p.pages["root|"] = &storagev1.ListResponse{Entries: entries, Complete: true}
	page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Directories) != 100 || page.NextCursor == "" {
		t.Fatalf("first bounded host page: %d", len(page.Directories))
	}
	page, err = b.Browse(t.Context(), a, s.Key, BrowseRequest{DirectoryRef: page.Current.SelectionRef, Cursor: page.NextCursor, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Directories) != 100 || page.NextCursor == "" {
		t.Fatalf("second bounded host page: %d", len(page.Directories))
	}
	page, err = b.Browse(t.Context(), a, s.Key, BrowseRequest{DirectoryRef: page.Current.SelectionRef, Cursor: page.NextCursor, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Directories) != 12 || page.NextCursor != "" {
		t.Fatalf("final bounded host page: %d", len(page.Directories))
	}
}
func TestSourceBrowserAudioRequiresExplicitDirectoryListingDB(t *testing.T) {
	for _, directoryOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-intent", true: "directories"}[directoryOnly], func(t *testing.T) {
			b, a, s, _ := newBrowseFixture(t)
			r := b.runtime.(browseRuntime)
			r.description.Sources[0].MediaKinds = []string{"audiobooks"}
			r.description.Sources[0].DirectoryOnlyListing = directoryOnly
			page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{})
			if directoryOnly {
				if err != nil || len(page.Directories) != 2 {
					t.Fatalf("safe audio browse: %v", err)
				}
			} else if err == nil {
				t.Fatal("audio browse admitted file hydration")
			}
		})
	}
}
func TestSourceBrowserRejectsDuplicateAcrossProviderPagesDB(t *testing.T) {
	b, a, s, p := newBrowseFixture(t)
	p.pages["root|"] = &storagev1.ListResponse{Entries: []*storagev1.Entry{browseEntry("nested")}, NextCursor: "next"}
	p.pages["root|next"] = &storagev1.ListResponse{Entries: []*storagev1.Entry{browseEntry("nested")}, Complete: true}
	page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Browse(t.Context(), a, s.Key, BrowseRequest{DirectoryRef: page.Current.SelectionRef, Cursor: page.NextCursor, Limit: 1}); err == nil {
		t.Fatal("cross-page duplicate accepted")
	}
}
func TestPreparedSelectionsCannotBeCopiedOrReusedAfterRevocationDB(t *testing.T) {
	for _, effect := range []string{"copy", "actor", "configuration", "expired", "file-directory"} {
		t.Run(effect, func(t *testing.T) {
			b, a, s, provider := newBrowseFixture(t)
			page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{})
			if err != nil {
				t.Fatal(err)
			}
			l := NewLibraryLocations(b.pool)
			l.SetSourceBrowser(b)
			p, err := l.PrepareSelections(t.Context(), a, []RootSelection{{SourceKey: s.Key, ConfigurationRevision: 1, SelectionRef: page.Current.SelectionRef}})
			if err != nil {
				t.Fatal(err)
			}
			var folder int
			if err := b.pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name) VALUES('ebooks','Prepared authority') RETURNING id`).Scan(&folder); err != nil {
				t.Fatal(err)
			}
			validTx, err := b.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := l.RetainPreparedTx(t.Context(), validTx, a, folder, p); err != nil {
				validTx.Rollback(context.Background())
				t.Fatal("valid prepared evidence rejected", err)
			}
			validTx.Rollback(context.Background())
			switch effect {
			case "copy":
				copy := *p
				p = &copy
			case "actor":
				a.SessionID = uuid.NewString()
			case "configuration":
				_, err = b.pool.Exec(t.Context(), `UPDATE bloem_storage_sources SET configuration_revision=configuration_revision+1 WHERE key=$1`, s.Key)
			case "expired":
				b.now = func() time.Time { return time.Now().Add(time.Hour) }
			case "file-directory":
				provider.entries["root"].Kind = storagev1.EntryKind_ENTRY_KIND_FILE
				_, err = l.PrepareSelections(t.Context(), a, []RootSelection{{SourceKey: s.Key, ConfigurationRevision: 1, SelectionRef: page.Current.SelectionRef}})
				if err == nil {
					t.Fatal("file prepared as a directory")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tx, err := b.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = l.RetainPreparedTx(t.Context(), tx, a, folder, p); err == nil {
				t.Fatal("invalid prepared evidence accepted")
			}
		})
	}
}
func TestSourceBrowserReferencesRejectOtherAuthorizedSourceDB(t *testing.T) {
	b, a, s, _ := newBrowseFixture(t)
	page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{})
	if err != nil {
		t.Fatal(err)
	}
	other := s
	other.Key = uuid.Nil
	other.ProviderSourceID = "other-authorized-source"
	other, err = storagesource.NewRepository(b.pool).CreateSource(t.Context(), other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Browse(t.Context(), a, other.Key, BrowseRequest{DirectoryRef: page.Current.SelectionRef}); err == nil {
		t.Fatal("cross-source authenticated ref accepted")
	}
}

func TestSourceBrowserCompactCursorRejectsChangedOrMissingStateDB(t *testing.T) {
	for _, effect := range []string{"changed-page", "eviction", "tampered-state", "other-browser"} {
		t.Run(effect, func(t *testing.T) {
			b, a, s, p := newBrowseFixture(t)
			for _, entry := range p.pages["root|"].Entries {
				entry.Name = strings.Repeat("N", 4000)
				entry.LogicalPath = strings.Repeat("P", 32000)
			}
			page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			query := url.Values{"directory_ref": {page.Current.SelectionRef}, "cursor": {page.NextCursor}, "limit": {"1"}}.Encode()
			if len(query) > 6144 {
				t.Fatalf("cursor cannot fit an ordinary GET request: %d bytes", len(query))
			}
			store := b.cursorStore.(*browseMemoryStore)
			switch effect {
			case "changed-page":
				p.pages["root|"].Entries[0].Name = "Changed"
			case "eviction":
				store.mu.Lock()
				store.values = map[string][]byte{}
				store.mu.Unlock()
			case "tampered-state":
				store.mu.Lock()
				for key := range store.values {
					store.values[key] = []byte("altered state")
				}
				store.mu.Unlock()
			case "other-browser":
				copy := *b
				b = &copy
			}
			next, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{DirectoryRef: page.Current.SelectionRef, Cursor: page.NextCursor, Limit: 1})
			if effect == "other-browser" {
				if err != nil || len(next.Directories) != 1 {
					t.Fatalf("cluster-shared continuation: %v", err)
				}
				return
			}
			var typed *catalog.NativeOnboardingError
			if !errors.As(err, &typed) || typed.Code != "browse_restart_required" {
				t.Fatalf("stale cursor: %v", err)
			}
		})
	}
}
func TestSourceBrowserWithoutCursorStoreKeepsTerminalBrowseDB(t *testing.T) {
	b, a, s, _ := newBrowseFixture(t)
	b.SetCursorStore(nil)
	if page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{}); err != nil || len(page.Directories) != 2 {
		t.Fatalf("terminal browse without Redis: %v", err)
	}
	if _, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{Limit: 1}); err == nil {
		t.Fatal("pagination without shared state succeeded")
	}
}
func TestSourceBrowserRechecksHeldCursorStateDB(t *testing.T) {
	b, a, s, _ := newBrowseFixture(t)
	page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	store := b.cursorStore.(*browseMemoryStore)
	started, release := make(chan struct{}), make(chan struct{})
	store.mu.Lock()
	store.started = started
	store.release = release
	store.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := b.Browse(ctx, a, s.Key, BrowseRequest{DirectoryRef: page.Current.SelectionRef, Cursor: page.NextCursor, Limit: 1})
		result <- err
	}()
	select {
	case <-started:
	case err := <-result:
		t.Fatalf("cursor redemption did not start: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := b.pool.Exec(ctx, `SELECT 1`); err != nil {
		close(release)
		t.Fatal("Redis wait held database connection", err)
	}
	_, err = b.pool.Exec(ctx, `UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE id=$1`, a.SessionID)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Redis pagination state bypassed revoked authority")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
func TestSourceBrowserReferencesRejectOtherAuthorizedOrganizationDB(t *testing.T) {
	b, a, s, _ := newBrowseFixture(t)
	org, membership, policy, security := discoveryOrganization(t, b.pool, a.AccountID)
	a.Scope = auth.AdminScopeOrganization
	a.OrganizationID = org
	a.MembershipID = membership
	a.PolicyRevision = policy
	a.SecurityRevision = security
	a.EffectiveAuthority = "organization_admin"
	if _, err := b.pool.Exec(t.Context(), `INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) VALUES($1,'plugin_availability','plugin_installation',$2,$3,'active','browse-test')`, org, s.OwnerID, s.InstallationID); err != nil {
		t.Fatal(err)
	}
	page, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.pool.Exec(t.Context(), `UPDATE organizations SET slug='first-browse-org' WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	org, membership, policy, security = discoveryOrganization(t, b.pool, a.AccountID)
	a.OrganizationID = org
	a.MembershipID = membership
	a.PolicyRevision = policy
	a.SecurityRevision = security
	if _, err := b.pool.Exec(t.Context(), `INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) VALUES($1,'plugin_availability','plugin_installation',$2,$3,'active','browse-test')`, org, s.OwnerID, s.InstallationID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{}); err != nil {
		t.Fatal("second organization is independently authorized", err)
	}
	if _, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{DirectoryRef: page.Current.SelectionRef}); err == nil {
		t.Fatal("first organization's reference accepted by second organization")
	}
}

func TestSourceBrowserRedisPaginationBeyondActorQuotaDB(t *testing.T) {
	b, a, s, p := newBrowseFixture(t)
	store, client := browseRedisFixture(t)
	b.SetCursorStore(store)
	actorKey := browseActorKey(a)
	keys := []string{}
	t.Cleanup(func() {
		for _, key := range keys {
			store.Delete(context.Background(), key, actorKey)
		}
	})
	for i := 0; i < 20; i++ {
		cursor := ""
		if i > 0 {
			cursor = fmt.Sprintf("page-%d", i)
		}
		page := &storagev1.ListResponse{Entries: []*storagev1.Entry{browseEntry(fmt.Sprintf("directory-%d", i))}, Complete: i == 19}
		if !page.Complete {
			page.NextCursor = fmt.Sprintf("page-%d", i+1)
		}
		p.pages["root|"+cursor] = page
	}
	request := BrowseRequest{Limit: 1}
	for i := 0; i < 20; i++ {
		page, err := b.Browse(t.Context(), a, s.Key, request)
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		if len(page.Directories) != 1 || page.Directories[0].Name != fmt.Sprintf("directory-%d", i) {
			t.Fatalf("page %d repeated or skipped: %+v", i, page)
		}
		request.DirectoryRef = page.Current.SelectionRef
		request.Cursor = page.NextCursor
		if page.NextCursor != "" {
			var cursor browseCursor
			if err := b.openBrowse(page.NextCursor, cursorPurpose, &cursor); err != nil {
				t.Fatal(err)
			}
			keys = append(keys, browseStateKey(cursor.StateID))
		}
		if count := client.ZCard(t.Context(), browseStatePrefix+"actor:"+actorKey).Val(); count > 1 {
			t.Fatalf("serial pagination retained %d states", count)
		}
	}
	if request.Cursor != "" {
		t.Fatal("terminal provider page left a cursor")
	}
}

// Remote storage alone is replaced; encryption, token bindings and replay/page
// validation all run in the production browser.
type browseMemoryStore struct {
	mu      sync.Mutex
	values  map[string][]byte
	actors  map[string]string
	started chan struct{}
	release <-chan struct{}
}

func newBrowseMemoryStore() *browseMemoryStore {
	return &browseMemoryStore{values: map[string][]byte{}, actors: map[string]string{}}
}
func (s *browseMemoryStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	value := append([]byte(nil), s.values[key]...)
	started, release := s.started, s.release
	s.started = nil
	s.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return value, nil
}
func (s *browseMemoryStore) Put(_ context.Context, key, actor, replace string, data []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.values[key]; exists {
		return errors.New("immutable state exists")
	}
	if replace != "" && (s.actors[replace] != actor || len(s.values[replace]) == 0) {
		return errBrowseCursorRestart
	}
	s.values[key] = append([]byte(nil), data...)
	s.actors[key] = actor
	delete(s.values, replace)
	delete(s.actors, replace)
	return nil
}
func (s *browseMemoryStore) Delete(_ context.Context, key, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	delete(s.actors, key)
	return nil
}
