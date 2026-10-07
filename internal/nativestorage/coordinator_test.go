package nativestorage

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

type refsStub struct {
	source storagesource.SourceConfig
	ref    storagesource.PersistedRef
	err    error
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
func (s *sourceStub) ReadRange(ctx context.Context, _ mediasource.Ref, _, _ int64, _ io.Writer) error {
	return ctx.Err()
}

type runtimeStub struct {
	source   *sourceStub
	ctx      context.Context
	snapshot storageplugin.Snapshot
	calls    int
	disabled int
	after    func()
}

func (s *runtimeStub) Borrow(_ context.Context, r storageplugin.Snapshot) (Lease, error) {
	s.calls++
	s.snapshot = r
	if s.after != nil {
		s.after()
	}
	return Lease{Context: s.ctx, Source: s.source}, nil
}
func (s *runtimeStub) Disable(int) { s.disabled++ }
func fixture(t *testing.T) (*Coordinator, *refsStub, *registryStub, *runtimeStub, *models.MediaFile) {
	t.Helper()
	id := int64(3)
	r := &refsStub{source: storagesource.SourceConfig{Key: uuid.New(), OwnerID: uuid.New(), InstallationID: &id, PluginID: "native", ProviderSourceID: "retained-source", ConfigurationRevision: 2, Enabled: true}, ref: storagesource.PersistedRef{LocationID: uuid.New(), EntryID: "retained-entry", Revision: "retained-revision", LogicalPath: "book.epub"}}
	location, err := storagesource.CatalogLocation(r.ref.LocationID, r.ref.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	f := &models.MediaFile{ID: 7, MediaFolderID: 9, ContentID: "book", FilePath: location}
	reg := &registryStub{snapshot: &plugins.NativeStorageSnapshot{Source: r.source, Installation: &plugins.Installation{ID: 3, InstallPath: "/approved/plugin", Enabled: true}, Generation: 5, ArtifactChecksum: "reviewed"}}
	run := &runtimeStub{source: &sourceStub{}, ctx: context.Background()}
	c := &Coordinator{References: r, Registry: reg, Runtime: run, AuthorizeFile: func(context.Context, *models.MediaFile) (*models.MediaFile, error) { return f, nil }, AuthorizeSource: func(context.Context, *models.MediaFile, storagesource.SourceConfig) error { return nil }}
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
