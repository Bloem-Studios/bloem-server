package nativestorage

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

type refsStub struct {
	source   storagesource.SourceConfig
	ref      storagesource.PersistedRef
	err      error
	cover    storagesource.CoverReference
	coverErr error
}

func (s *refsStub) CoverReference(_ context.Context, contentID, revision string) (storagesource.CoverReference, error) {
	if contentID != "book" || revision != artworkkey.StorageCoverRevision("cover/book", "cover:1") {
		return storagesource.CoverReference{}, storagesource.ErrReferenceConflict
	}
	return s.cover, s.coverErr
}
func (s *refsStub) FileReference(_ context.Context, id, folder int) (storagesource.SourceConfig, storagesource.PersistedRef, error) {
	if id != 7 || folder != 9 {
		return s.source, s.ref, storagesource.ErrReferenceConflict
	}
	return s.source, s.ref, s.err
}

type registryStub struct {
	snapshot *plugins.NativeStorageSnapshot
	calls    int
}

func (s *registryStub) Snapshot(context.Context, uuid.UUID, uuid.UUID) (*plugins.NativeStorageSnapshot, error) {
	s.calls++
	return s.snapshot, nil
}

type sourceStub struct {
	got mediasource.Ref
	ctx context.Context
}

func (s *sourceStub) Stat(ctx context.Context, r mediasource.Ref) (mediasource.Info, error) {
	s.got = r
	s.ctx = ctx
	return mediasource.Info{Name: "book.epub", LogicalPath: "book.epub", Revision: r.Revision, Size: 4}, ctx.Err()
}
func (s *sourceStub) ReadRange(ctx context.Context, _ mediasource.Ref, offset, length int64, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := w.Write([]byte("book")[offset : offset+length])
	return err
}

type runtimeStub struct {
	source      *sourceStub
	description *storagev1.DescribeResponse
	ctx         context.Context
	snapshot    storageplugin.Snapshot
	calls       int
	disabled    int
	after       func()
}

func (s *runtimeStub) Borrow(_ context.Context, r storageplugin.Snapshot) (Lease, error) {
	s.calls++
	s.snapshot = r
	if s.after != nil {
		s.after()
	}
	return Lease{Context: s.ctx, Source: s.source, Description: s.description}, nil
}
func (s *runtimeStub) Disable(int) { s.disabled++ }
func fixture(t *testing.T) (*Coordinator, *refsStub, *registryStub, *runtimeStub, *models.MediaFile) {
	t.Helper()
	id := int64(3)
	r := &refsStub{source: storagesource.SourceConfig{Key: uuid.New(), OwnerID: uuid.New(), InstallationID: &id, PluginID: "native", ProviderSourceID: "retained-source", RootEntryID: "retained-root", ConfigurationRevision: 2, Enabled: true}, ref: storagesource.PersistedRef{LocationID: uuid.New(), EntryID: "retained-entry", Revision: "retained-revision", LogicalPath: "book.epub"}}
	location, err := storagesource.CatalogLocation(r.ref.LocationID, r.ref.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	f := &models.MediaFile{ID: 7, MediaFolderID: 9, ContentID: "book", FilePath: location}
	coverLocation := storagesource.Location{ID: r.ref.LocationID, SourceKey: r.source.Key, FolderID: 9}
	r.cover = storagesource.CoverReference{MediaFileID: 7, FolderID: 9, ContentID: "book", FilePath: location, Location: coverLocation, Source: r.source,
		Cover: storagesource.PersistedRef{LocationID: r.ref.LocationID, EntryID: "cover/book", Revision: "cover:1"}}
	reg := &registryStub{snapshot: &plugins.NativeStorageSnapshot{Source: r.source, Installation: &plugins.Installation{ID: 3, InstallPath: "/approved/plugin", Enabled: true}, Generation: 5, ArtifactChecksum: "reviewed"}}
	run := &runtimeStub{source: &sourceStub{}, ctx: context.Background(), description: &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: r.source.ProviderSourceID, RootEntryId: r.source.RootEntryID, RevisionPinnedReads: true}}}}
	c := &Coordinator{References: r, Registry: reg, Runtime: run, AuthorizeFile: func(context.Context, *models.MediaFile) (*models.MediaFile, error) { return f, nil }, AuthorizeSource: func(context.Context, *models.MediaFile, storagesource.SourceConfig) error { return nil },
		AuthorizeCover: func(context.Context, storagesource.Location, storagesource.SourceConfig) error { return nil }}
	return c, r, reg, run, f
}
func TestOpenUsesRetainedReferenceAndFreshSnapshot(t *testing.T) {
	c, r, reg, run, f := fixture(t)
	opened, err := c.OpenAuthorizedEbook(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if run.source.got != (mediasource.Ref{SourceID: r.source.ProviderSourceID, EntryID: r.ref.EntryID, Revision: r.ref.Revision}) {
		t.Fatalf("reference=%+v", run.source.got)
	}
	if reg.calls < 2 || run.snapshot.InstallationID != 3 || run.snapshot.Generation != 5 || !run.snapshot.NativeOnly {
		t.Fatalf("snapshot=%+v checks=%d", run.snapshot, reg.calls)
	}
}

func TestOpenDescribeAdmissionBeforeStat(t *testing.T) {
	for _, surface := range []string{"ebook", "cover"} {
		for _, kind := range []string{"absent", "source", "root", "unpinned", "duplicate", "malformed-other"} {
			t.Run(surface+"/"+kind, func(t *testing.T) {
				c, _, _, run, f := fixture(t)
				switch kind {
				case "absent":
					run.description = nil
				case "source":
					run.description.Sources[0].Id = "different-source"
				case "root":
					run.description.Sources[0].RootEntryId = "different-root"
				case "unpinned":
					run.description.Sources[0].RevisionPinnedReads = false
				case "duplicate":
					run.description.Sources = append(run.description.Sources, run.description.Sources[0])
				case "malformed-other":
					run.description.Sources = append(run.description.Sources, &storagev1.Source{Id: "other"})
				}
				var opened mediasource.File
				var err error
				if surface == "ebook" {
					opened, err = c.OpenAuthorizedEbook(t.Context(), f)
				} else {
					opened, err = c.OpenCover(t.Context(), "book", artworkkey.StorageCoverRevision("cover/book", "cover:1"))
				}
				if opened != nil {
					opened.Close()
				}
				if !errors.Is(err, storagesource.ErrSourceUnavailable) || opened != nil || run.source.ctx != nil {
					t.Fatalf("invalid Describe reached content: opened=%v stat=%v err=%v", opened != nil, run.source.ctx != nil, err)
				}
			})
		}
	}
}

func TestOpenRechecksAuthorityAfterDescribe(t *testing.T) {
	for _, surface := range []string{"ebook", "cover"} {
		for _, mutation := range []string{"root", "source", "generation", "checksum", "policy", "request-cancel", "session-cancel"} {
			t.Run(surface+"/"+mutation, func(t *testing.T) {
				c, refs, reg, run, file := fixture(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				session, cancelSession := context.WithCancel(t.Context())
				defer cancelSession()
				run.ctx = session
				run.after = func() {
					switch mutation {
					case "root":
						refs.source.RootEntryID = "changed-root"
						refs.cover.Source.RootEntryID = "changed-root"
						reg.snapshot.Source.RootEntryID = "changed-root"
					case "source":
						refs.source.ProviderSourceID = "changed-source"
						refs.cover.Source.ProviderSourceID = "changed-source"
						reg.snapshot.Source.ProviderSourceID = "changed-source"
					case "generation":
						fresh := *reg.snapshot
						fresh.Generation++
						reg.snapshot = &fresh
					case "checksum":
						fresh := *reg.snapshot
						fresh.ArtifactChecksum = "changed"
						reg.snapshot = &fresh
					case "policy":
						c.AuthorizeSource = func(context.Context, *models.MediaFile, storagesource.SourceConfig) error {
							return errors.New("revoked")
						}
						c.AuthorizeCover = func(context.Context, storagesource.Location, storagesource.SourceConfig) error {
							return errors.New("revoked")
						}
					case "request-cancel":
						cancel()
					case "session-cancel":
						cancelSession()
					}
				}
				var opened mediasource.File
				var err error
				if surface == "ebook" {
					opened, err = c.OpenAuthorizedEbook(ctx, file)
				} else {
					opened, err = c.OpenCover(ctx, "book", artworkkey.StorageCoverRevision("cover/book", "cover:1"))
				}
				if opened != nil {
					opened.Close()
				}
				if err == nil || opened != nil || run.source.ctx != nil {
					t.Fatalf("authority changed during Describe: opened=%v stat=%v err=%v", opened != nil, run.source.ctx != nil, err)
				}
			})
		}
	}
}
func TestOpenRejectsMismatchUnavailableAndUnauthorized(t *testing.T) {
	for _, kind := range []string{"catalog", "unavailable", "policy", "snapshot", "during-start"} {
		t.Run(kind, func(t *testing.T) {
			c, r, reg, run, f := fixture(t)
			copyFile := *f
			switch kind {
			case "catalog":
				copyFile.FilePath = "bloem-storage:forged"
			case "unavailable":
				r.err = storagesource.ErrSourceUnavailable
			case "policy":
				c.AuthorizeSource = func(context.Context, *models.MediaFile, storagesource.SourceConfig) error {
					return errors.New("denied")
				}
			case "snapshot":
				reg.snapshot.Source.ConfigurationRevision++
			case "during-start":
				run.after = func() { r.err = storagesource.ErrSourceUnavailable }
			}
			opened, err := c.OpenAuthorizedEbook(context.Background(), &copyFile)
			if err == nil || opened != nil {
				t.Fatal("accepted invalid source")
			}
			if kind != "during-start" && run.calls != 0 {
				t.Fatal("launched before authorization")
			}
		})
	}
}
func TestOpenLifetime(t *testing.T) {
	for _, kind := range []string{"request", "session", "close"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _, run, f := fixture(t)
			request, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			session, cancelSession := context.WithCancel(context.Background())
			defer cancelSession()
			run.ctx = session
			opened, err := c.OpenAuthorizedEbook(request, f)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			switch kind {
			case "request":
				cancelRequest()
			case "session":
				cancelSession()
			case "close":
				opened.Close()
			}
			<-run.source.ctx.Done()
			if _, err := opened.Read(make([]byte, 1)); err == nil {
				t.Fatal("read survived lifetime")
			}
		})
	}
}
func TestMutationFencesAmbiguousFailure(t *testing.T) {
	c, _, _, run, _ := fixture(t)
	sentinel := errors.New("commit outcome unknown")
	if err := c.FenceMutation(context.Background(), 3, func(context.Context) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if run.disabled != 2 {
		t.Fatalf("disable calls=%d", run.disabled)
	}
}
func TestCoordinatorRetainsHostUntilCloseAndReleasesFailedOpen(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "response", true: "failed-authorization"}[failed], func(t *testing.T) {
			c, refs, _, _, file := fixture(t)
			h := &Host{Manager: storageplugin.NewManager(storageplugin.Config{})}
			c.AcquireOpen = h.AcquireOpen
			if failed {
				refs.err = storagesource.ErrSourceUnavailable
			}
			opened, err := c.OpenAuthorizedEbook(context.Background(), file)
			if failed {
				if err == nil {
					t.Fatal("unexpected open")
				}
				if err = h.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err = h.Shutdown(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("reader not retained: %v", err)
			}
			if err = opened.Close(); err != nil {
				t.Fatal(err)
			}
			if err = h.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOpenCoverReadsTheSourcesCoverAtItsRevision(t *testing.T) {
	c, r, reg, run, _ := fixture(t)
	opened, err := c.OpenCover(context.Background(), "book", artworkkey.StorageCoverRevision("cover/book", "cover:1"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if run.source.got != (mediasource.Ref{SourceID: r.source.ProviderSourceID, EntryID: "cover/book", Revision: "cover:1"}) {
		t.Fatalf("reference=%+v", run.source.got)
	}
	if reg.calls < 2 || run.snapshot.Generation != 5 || !run.snapshot.NativeOnly {
		t.Fatalf("snapshot=%+v checks=%d", run.snapshot, reg.calls)
	}
}

func TestOpenCoverRejectsUnavailableAndUnauthorized(t *testing.T) {
	for _, kind := range []string{"stale", "unavailable", "policy", "disabled", "moved", "snapshot", "during-start"} {
		t.Run(kind, func(t *testing.T) {
			c, r, reg, run, _ := fixture(t)
			revision := artworkkey.StorageCoverRevision("cover/book", "cover:1")
			switch kind {
			case "stale":
				revision = "older-rev"
			case "unavailable":
				r.coverErr = storagesource.ErrSourceUnavailable
			case "policy":
				c.AuthorizeCover = func(context.Context, storagesource.Location, storagesource.SourceConfig) error {
					return errors.New("denied")
				}
			case "disabled":
				r.cover.Source.Enabled = false
			case "moved":
				r.cover.Location.SourceKey = uuid.New()
			case "snapshot":
				reg.snapshot.Source.ConfigurationRevision++
			case "during-start":
				run.after = func() { r.coverErr = storagesource.ErrSourceUnavailable }
			}
			opened, err := c.OpenCover(context.Background(), "book", revision)
			if err == nil || opened != nil {
				t.Fatal("served a cover from an invalid source")
			}
			if kind != "during-start" && run.calls != 0 {
				t.Fatal("launched before authorization")
			}
		})
	}
}

type coverCacheStub struct {
	mu   sync.Mutex
	data map[string][]byte
	gets int
}

func (c *coverCacheStub) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets++
	return c.data[key], nil
}
func (c *coverCacheStub) Set(_ context.Context, key string, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = data
	return nil
}

func TestHostReadsCoversThroughTheCache(t *testing.T) {
	c, refs, _, run, _ := fixture(t)
	h := &Host{}
	if _, err := h.ReadCover(context.Background(), "book", artworkkey.StorageCoverRevision("cover/book", "cover:1")); !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatalf("cover served before the router published its reader: %v", err)
	}
	h.SetCoverReader(c)
	ctx := WithCoverReadAuthority(context.Background(), refs.cover, func(ctx context.Context) (storagesource.CoverReference, error) {
		return refs.CoverReference(ctx, "book", artworkkey.StorageCoverRevision("cover/book", "cover:1"))
	})
	cache := &coverCacheStub{data: map[string][]byte{}}
	h.SetCoverCache(cache)
	for range 2 {
		data, err := h.ReadCover(ctx, "book", artworkkey.StorageCoverRevision("cover/book", "cover:1"))
		if err != nil || string(data) != "book" {
			t.Fatalf("cover = %q %v", data, err)
		}
	}
	// The second read came from the cache; the source was read once.
	if run.calls != 1 || cache.gets != 2 || len(cache.data) != 1 {
		t.Fatalf("source reads = %d, cache gets = %d, cached = %d", run.calls, cache.gets, len(cache.data))
	}
	// A stale revision is refused and nothing is cached for it.
	if _, err := h.ReadCover(ctx, "book", "older-rev"); err == nil || len(cache.data) != 1 {
		t.Fatalf("stale cover: %v, cached = %d", err, len(cache.data))
	}
}

func TestHostBoundsConcurrentCoverReads(t *testing.T) {
	c, refs, registry, run, _ := fixture(t)
	c.Registry = coverWaitRegistry{registry.snapshot}
	h := &Host{}
	h.covers.slots = make(chan struct{}, 1)
	h.SetCoverReader(c)
	ctx := WithCoverReadAuthority(context.Background(), refs.cover, func(ctx context.Context) (storagesource.CoverReference, error) {
		return refs.CoverReference(ctx, "book", artworkkey.StorageCoverRevision("cover/book", "cover:1"))
	})
	// With the only slot taken, a read waits until its caller gives up.
	h.covers.slots <- struct{}{}
	waiting, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := h.ReadCover(waiting, "book", artworkkey.StorageCoverRevision("cover/book", "cover:1")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read did not wait for a slot: %v", err)
	}
	<-h.covers.slots
	// Reads return their slot, including failed ones.
	for _, revision := range []string{"older-rev", artworkkey.StorageCoverRevision("cover/book", "cover:1"), artworkkey.StorageCoverRevision("cover/book", "cover:1")} {
		_, _ = h.ReadCover(ctx, "book", revision)
	}
	if len(h.covers.slots) != 0 || run.calls < 1 {
		t.Fatalf("slots held = %d, source reads = %d", len(h.covers.slots), run.calls)
	}
}
