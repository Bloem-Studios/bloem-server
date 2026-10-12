package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

type coverRecipeRefs struct {
	ref    storagesource.CoverReference
	denied bool
}

func (s *coverRecipeRefs) CoverReference(context.Context, string, string) (storagesource.CoverReference, error) {
	if s.denied {
		return s.ref, storagesource.ErrReferenceConflict
	}
	return s.ref, nil
}
func (s *coverRecipeRefs) CoverReferences(ctx context.Context, c, r string) ([]storagesource.CoverReference, error) {
	ref, err := s.CoverReference(ctx, c, r)
	if err != nil {
		return nil, err
	}
	return []storagesource.CoverReference{ref}, nil
}
func (s *coverRecipeRefs) CoverReferenceForFile(ctx context.Context, c, r string, id int, root uuid.UUID) (storagesource.CoverReference, error) {
	if id != s.ref.MediaFileID || root != s.ref.RootID {
		return s.ref, storagesource.ErrReferenceConflict
	}
	return s.CoverReference(ctx, c, r)
}

type coverRecipeRegistry struct {
	snapshot *plugins.NativeStorageSnapshot
	after    func()
}

func (s *coverRecipeRegistry) Snapshot(context.Context, uuid.UUID, uuid.UUID) (*plugins.NativeStorageSnapshot, error) {
	if s.after != nil {
		s.after()
	}
	return s.snapshot, nil
}

func TestStorageCoverRecipeBindsViewerAndExactPublication(t *testing.T) {
	for _, mutation := range []string{"account", "session", "profile", "pin", "tenant", "library", "source", "root", "file", "cover", "generation", "expiry", "tamper", "anonymous-signature", "session-during-snapshot"} {
		t.Run(mutation, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			f := d.base
			incarnation := uuid.New()
			user := &models.User{ID: 7, Enabled: true, AccountIncarnationID: incarnation}
			f.policy.loadUser = func(context.Context, int) (*models.User, error) { return user, nil }
			f.viewer.scope.PlaybackAllowed = false // artwork is not video admission
			f.file.Container = "epub"
			f.file.CodecVideo = ""
			f.policy.authorizeSource = func(context.Context, *models.MediaFile, storagesource.SourceConfig) error { return f.sourceErr }
			location := uuid.New()
			root := uuid.New()
			install := int64(3)
			source := storagesource.SourceConfig{Key: uuid.New(), OwnerID: uuid.New(), InstallationID: &install, Enabled: true, ConfigurationRevision: 1}
			ref := storagesource.CoverReference{MediaFileID: 11, FolderID: 9, ContentID: "book", FilePath: f.file.FilePath, Source: source, RootID: root, Location: storagesource.Location{ID: location, SourceKey: source.Key, FolderID: 9, SelectionRevision: 1}, Cover: storagesource.PersistedRef{LocationID: location, EntryID: "cover/book", Revision: "c1"}}
			refs := &coverRecipeRefs{ref: ref}
			registry := &coverRecipeRegistry{snapshot: &plugins.NativeStorageSnapshot{Source: source, Installation: &plugins.Installation{ID: 3, Enabled: true}, Generation: 1, ArtifactChecksum: "approved"}}
			cipher, err := secret.New([]byte(strings.Repeat("cover-test-key", 3)))
			if err != nil {
				t.Fatal(err)
			}
			signer := artworkurl.NewSigner("cover-route-secret", time.Hour)
			service := &nativeStorageArtwork{next: artworkurl.NewServerResolver(signer), signer: signer, cipher: cipher, actors: f.policy, references: refs, registry: registry, authorizeFile: func(context.Context, storagesource.CoverReference) error { return f.fileErr }}
			key := artworkkey.StorageCoverKey("book", "cover/book", "c1")
			resolved := service.ResolveURLs(f.requestContext(t, false), []string{key})[key]
			if resolved.URL == "" || !strings.Contains(resolved.URL, "recipe=") {
				t.Fatalf("no viewer recipe: %s", resolved.URL)
			}
			request := httptest.NewRequest("GET", resolved.URL, nil)
			rev := artworkkey.StorageCoverRevision("cover/book", "c1")
			if _, err = service.authorizeRequest(t.Context(), request, "book", rev); err != nil {
				t.Fatalf("fresh recipe: %v", err)
			}
			switch mutation {
			case "account":
				user.AccountIncarnationID = uuid.New()
			case "session":
				d.login.ExpiresAt = time.Now().Add(-time.Second)
			case "profile":
				d.profile = nil
			case "pin":
				d.profile.PINRevision++
			case "tenant":
				f.tenant.tenant.SecurityRevision++
			case "library":
				f.fileErr = storagesource.ErrReferenceConflict
			case "source":
				f.sourceErr = storagesource.ErrSourceUnavailable
			case "root":
				refs.denied = true
			case "file":
				refs.ref.FilePath = "different"
			case "cover":
				refs.ref.Cover.Revision = "c2"
			case "generation":
				registry.snapshot.Generation++
			case "expiry":
				service.now = func() time.Time { return resolved.ExpiresAt.Add(time.Second) }
			case "tamper":
				q := request.URL.Query()
				q.Set("recipe", q.Get("recipe")+"x")
				request.URL.RawQuery = q.Encode()
			case "anonymous-signature":
				request = httptest.NewRequest("GET", strings.Split(resolved.URL, "&recipe=")[0], nil)
			case "session-during-snapshot":
				registry.after = func() { now := time.Now(); d.login.RevokedAt = &now }
			}
			if _, err = service.authorizeRequest(t.Context(), request, "book", rev); err == nil {
				t.Fatal("revoked recipe admitted")
			}
			if local := service.ResolveURLs(context.Background(), []string{"local.webp"})["local.webp"]; local.URL == "" || strings.Contains(local.URL, "recipe=") {
				t.Fatalf("ordinary artwork changed: %+v", local)
			}
		})
	}
}

var _ nativestorage.Registry = (*coverRecipeRegistry)(nil)

// reviewCoverFixture issues one fresh recipe for an EPUB cover through the
// production resolver, with a canonical publication path.
func reviewCoverFixture(t *testing.T) (*nativeStorageArtwork, *nativeVideoDelegationFixture, *coverRecipeRefs, *coverRecipeRegistry, string) {
	t.Helper()
	d := newNativeVideoDelegationFixture(t)
	f := d.base
	user := &models.User{ID: 7, Enabled: true, AccountIncarnationID: uuid.New()}
	f.policy.loadUser = func(context.Context, int) (*models.User, error) { return user, nil }
	f.viewer.scope.PlaybackAllowed = false
	f.policy.authorizeSource = func(context.Context, *models.MediaFile, storagesource.SourceConfig) error { return nil }
	location, root := uuid.New(), uuid.New()
	install := int64(3)
	source := storagesource.SourceConfig{Key: uuid.New(), OwnerID: uuid.New(), InstallationID: &install, Enabled: true, ConfigurationRevision: 1}
	refs := &coverRecipeRefs{ref: storagesource.CoverReference{MediaFileID: 11, FolderID: 9, ContentID: "book", Source: source, RootID: root, Location: storagesource.Location{ID: location, SourceKey: source.Key, FolderID: 9, SelectionRevision: 1}, Cover: storagesource.PersistedRef{LocationID: location, EntryID: "cover/book", Revision: "c1"}}}
	refs.ref.File = storagesource.PersistedRef{LocationID: location, EntryID: "book", Revision: "v1", LogicalPath: "book.epub"}
	refs.ref.FilePath, _ = storagesource.CatalogLocation(location, "book")
	reg := &coverRecipeRegistry{snapshot: &plugins.NativeStorageSnapshot{Source: source, Installation: &plugins.Installation{ID: 3, Enabled: true}, Generation: 1, ArtifactChecksum: "approved"}}
	cipher, err := secret.New([]byte(strings.Repeat("review-cover-test", 3)))
	if err != nil {
		t.Fatal(err)
	}
	signer := artworkurl.NewSigner("review-cover-secret", time.Hour)
	s := &nativeStorageArtwork{next: artworkurl.NewServerResolver(signer), signer: signer, cipher: cipher, actors: f.policy, references: refs, registry: reg, authorizeFile: func(context.Context, storagesource.CoverReference) error { return nil }}
	key := artworkkey.StorageCoverKey("book", "cover/book", "c1")
	resolved, ok := s.ResolveURLFor(f.requestContext(t, false), key, 0)
	if !ok {
		t.Fatal("fresh recipe missing")
	}
	return s, d, refs, reg, resolved.URL
}

// serveCoverRecipe sends one request through the production HTTP artwork hook
// and the production Host authorizer.
func serveCoverRecipe(t *testing.T, s *nativeStorageArtwork, raw, etag string) *httptest.ResponseRecorder {
	t.Helper()
	host := &nativestorage.Host{}
	host.SetCoverRequestAuthorizer(s.authorizeRequest)
	store, err := blobstore.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := apiv2.NewBloemArtworkHandler(store, s.signer, nil, host)
	req := httptest.NewRequest(http.MethodGet, raw, nil)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestReviewTask8LateAuthorityWaitRejects304 keeps the Task8 review proof: a
// session revocation or runtime upgrade during the final catalog wait must not
// yield a conditional 304.
func TestReviewTask8LateAuthorityWaitRejects304(t *testing.T) {
	for _, mutation := range []string{"session", "generation"} {
		t.Run(mutation, func(t *testing.T) {
			s, d, _, registry, raw := reviewCoverFixture(t)
			calls := 0
			s.authorizeFile = func(context.Context, storagesource.CoverReference) error {
				calls++
				if calls == 2 {
					if mutation == "session" {
						now := time.Now()
						d.login.RevokedAt = &now
					} else {
						fresh := *registry.snapshot
						fresh.Generation++
						registry.snapshot = &fresh
					}
				}
				return nil
			}
			rec := serveCoverRecipe(t, s, raw, `"`+artworkkey.StorageCoverRevision("cover/book", "c1")+`"`)
			if calls != 2 {
				t.Fatalf("fixture did not reach final catalog authority: calls=%d", calls)
			}
			if rec.Code != http.StatusNotFound {
				t.Fatalf("revocation during final catalog wait escaped: status=%d want404 mutation=%s", rec.Code, mutation)
			}
		})
	}
}

// TestStorageCoverRecipeRejectsChangesDuringFinalWaits commits each change
// during each final catalog, resource and registry wait of one conditional
// request. Actor, scope, runtime and expiry evidence are read after them.
func TestStorageCoverRecipeRejectsChangesDuringFinalWaits(t *testing.T) {
	for _, point := range []string{"reference", "file", "source", "registry"} {
		for _, mutation := range []string{"none", "session", "login-expiry", "pin", "library-scope", "generation", "recipe-expiry"} {
			t.Run(point+"/"+mutation, func(t *testing.T) {
				s, d, refs, registry, raw := reviewCoverFixture(t)
				etag := `"` + artworkkey.StorageCoverRevision("cover/book", "c1") + `"`
				if fresh := serveCoverRecipe(t, s, raw, etag); fresh.Code != http.StatusNotModified {
					t.Fatalf("fresh conditional=%d", fresh.Code)
				}
				expires := time.Now().Add(coverRecipeTTL)
				mutate := func() {
					switch mutation {
					case "session":
						now := time.Now()
						d.login.RevokedAt = &now
					case "login-expiry":
						d.login.ExpiresAt = time.Now().Add(-time.Second)
					case "pin":
						d.profile.PINRevision++
					case "library-scope":
						d.base.viewer.scope.LibrariesRestricted = true
						d.base.viewer.scope.AllowedLibraryIDs = []int{999}
					case "generation":
						fresh := *registry.snapshot
						fresh.Generation++
						registry.snapshot = &fresh
					case "recipe-expiry":
						s.now = func() time.Time { return expires.Add(time.Minute) }
					}
				}
				// Each request authorizes in two passes; the second call at a
				// wait point is the final pass.
				calls := 0
				hit := func() {
					calls++
					if calls == 2 {
						mutate()
					}
				}
				switch point {
				case "reference":
					s.references = &hookedCoverRefs{coverRecipeRefs: refs, after: hit}
				case "file":
					s.authorizeFile = func(context.Context, storagesource.CoverReference) error { hit(); return nil }
				case "source":
					s.actors.authorizeSource = func(context.Context, *models.MediaFile, storagesource.SourceConfig) error { hit(); return nil }
				case "registry":
					registry.after = hit
				}
				rec := serveCoverRecipe(t, s, raw, etag)
				if calls < 2 {
					t.Fatalf("fixture did not reach final %s wait: calls=%d", point, calls)
				}
				if mutation == "none" {
					if rec.Code != http.StatusNotModified {
						t.Fatalf("unchanged authority through final %s wait: status=%d", point, rec.Code)
					}
					return
				}
				if rec.Code != http.StatusNotFound || rec.Header().Get("ETag") != "" {
					t.Fatalf("%s during final %s wait escaped: status=%d etag=%q", mutation, point, rec.Code, rec.Header().Get("ETag"))
				}
			})
		}
	}
}

type hookedCoverRefs struct {
	*coverRecipeRefs
	after func()
}

func (s *hookedCoverRefs) CoverReferenceForFile(ctx context.Context, c, r string, id int, root uuid.UUID) (storagesource.CoverReference, error) {
	ref, err := s.coverRecipeRefs.CoverReferenceForFile(ctx, c, r, id, root)
	s.after()
	return ref, err
}

type reviewMultipleCoverRefs struct {
	refs []storagesource.CoverReference
}

func (s reviewMultipleCoverRefs) CoverReferences(context.Context, string, string) ([]storagesource.CoverReference, error) {
	return s.refs, nil
}
func (s reviewMultipleCoverRefs) CoverReferenceForFile(_ context.Context, _ string, _ string, id int, root uuid.UUID) (storagesource.CoverReference, error) {
	for _, ref := range s.refs {
		if ref.MediaFileID == id && ref.RootID == root {
			return ref, nil
		}
	}
	return storagesource.CoverReference{}, storagesource.ErrReferenceConflict
}

// TestReviewTask8AuthorizedAlternatePublicationGetsRecipe keeps the Task8
// review proof: when one source/cover is published into two libraries, a
// viewer denied the first still receives a recipe pinned to the second.
func TestReviewTask8AuthorizedAlternatePublicationGetsRecipe(t *testing.T) {
	s, d, first, registry, _ := reviewCoverFixture(t)
	second := first.ref
	second.MediaFileID = 12
	second.FolderID = 10
	second.Location.ID = uuid.New()
	second.Location.FolderID = 10
	second.RootID = uuid.New()
	second.Cover.LocationID = second.Location.ID
	second.File.LocationID = second.Location.ID
	second.FilePath, _ = storagesource.CatalogLocation(second.Location.ID, second.File.EntryID)
	s.references = reviewMultipleCoverRefs{[]storagesource.CoverReference{first.ref, second}}
	s.authorizeFile = func(_ context.Context, ref storagesource.CoverReference) error {
		if ref.FolderID == 9 {
			return storagesource.ErrReferenceConflict
		}
		return nil
	}
	ctx := d.base.requestContext(t, false)
	actor, err := s.captureActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	revision := artworkkey.StorageCoverRevision("cover/book", "c1")
	recipe := coverRecipe{Version: 1, Actor: actor, FileID: second.MediaFileID, FolderID: second.FolderID, LocationID: second.Location.ID, RootID: second.RootID, ContentID: "book", Revision: revision, ReferenceDigest: coverReferenceDigest(second), Generation: registry.snapshot.Generation, Checksum: registry.snapshot.ArtifactChecksum}
	if _, err = s.authorize(ctx, recipe); err != nil {
		t.Fatalf("alternate publication is not authorized: %v", err)
	}
	resolved, ok := s.ResolveURLFor(ctx, artworkkey.StorageCoverKey("book", "cover/book", "c1"), 0)
	if !ok || resolved.URL == "" {
		t.Fatal("authorized second library publication lost its cover because first library is denied")
	}
	// The issued recipe is pinned to the authorized second publication.
	if rec := serveCoverRecipe(t, s, resolved.URL, `"`+revision+`"`); rec.Code != http.StatusNotModified {
		t.Fatalf("second publication recipe conditional=%d", rec.Code)
	}
	s.references = reviewMultipleCoverRefs{[]storagesource.CoverReference{first.ref}}
	if rec := serveCoverRecipe(t, s, resolved.URL, `"`+revision+`"`); rec.Code != http.StatusNotFound {
		t.Fatalf("recipe switched to another publication after its own was removed: %d", rec.Code)
	}
}
