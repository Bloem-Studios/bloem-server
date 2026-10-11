package nativestorage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"google.golang.org/protobuf/types/known/structpb"
)

func playbackConfig(value string) *structpb.Struct {
	return &structpb.Struct{Fields: map[string]*structpb.Value{"fixture": structpb.NewStringValue(value)}}
}

// These fixtures replace only catalog and provider I/O. The production reader,
// range partitioning, host ownership and cancellation all run unchanged.
type playbackSourceFixture struct {
	data      []byte
	path      string
	statHook  func()
	rangeHook func(context.Context, int)
	rangeErr  error
	ranges    int
}

func (s *playbackSourceFixture) Stat(ctx context.Context, ref mediasource.Ref) (mediasource.Info, error) {
	if s.statHook != nil {
		s.statHook()
	}
	return mediasource.Info{Name: "movie.mp4", LogicalPath: s.path, Revision: ref.Revision, Size: int64(len(s.data))}, ctx.Err()
}
func (s *playbackSourceFixture) ReadRange(ctx context.Context, ref mediasource.Ref, offset, length int64, w io.Writer) error {
	s.ranges++
	if s.rangeHook != nil {
		s.rangeHook(ctx, s.ranges)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := w.Write(s.data[offset : offset+length]); err != nil {
		return err
	}
	return s.rangeErr
}

type playbackRuntimeFixture struct {
	lease    Lease
	after    func()
	snapshot storageplugin.Snapshot
}

func (r *playbackRuntimeFixture) Borrow(_ context.Context, snapshot storageplugin.Snapshot) (Lease, error) {
	r.snapshot = snapshot
	if r.after != nil {
		r.after()
	}
	return r.lease, nil
}
func (*playbackRuntimeFixture) Disable(int) {}

func playbackFixture(t *testing.T) (*Coordinator, *refsStub, *registryStub, *playbackRuntimeFixture, *playbackSourceFixture, *models.MediaFile, PlaybackAuthority) {
	t.Helper()
	c, refs, registry, runtime, file := fixture(t)
	file.ContentID = "movie"
	refs.ref.LogicalPath = "movie.mp4"
	source := &playbackSourceFixture{data: []byte("movie"), path: "movie.mp4"}
	r := &playbackRuntimeFixture{lease: Lease{Context: context.Background(), Source: source, Description: runtime.description}}
	c.Runtime = r
	// Movie authority must not call the ebook-only shared callbacks.
	c.AuthorizeFile = func(context.Context, *models.MediaFile) (*models.MediaFile, error) {
		t.Fatal("ebook file authorizer used")
		return nil, nil
	}
	c.AuthorizeSource = func(context.Context, *models.MediaFile, storagesource.SourceConfig) error {
		t.Fatal("ebook source authorizer used")
		return nil
	}
	a := PlaybackAuthority{AuthorizeFile: func(_ context.Context, f *models.MediaFile) (*models.MediaFile, error) {
		actual := *file
		return &actual, nil
	}, AuthorizeSource: func(context.Context, *models.MediaFile, storagesource.SourceConfig) error { return nil }}
	return c, refs, registry, r, source, file, a
}

func TestPlaybackReadsPinnedMovieWithSeparateAuthority(t *testing.T) {
	c, _, _, runtime, _, file, a := playbackFixture(t)
	f, err := c.OpenAuthorizedPlayback(t.Context(), file, a)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	identity := f.Identity()
	if identity.FileID != 7 || identity.MediaFolderID != 9 || identity.ContentID != "movie" || identity.Reference.EntryID != "retained-entry" || identity.Reference.Revision != "retained-revision" || identity.Generation != 5 || identity.ArtifactChecksum != "reviewed" || runtime.snapshot.Generation != 5 {
		t.Fatalf("identity=%+v", identity)
	}
	data := make([]byte, 8)
	n, err := f.ReadAt(data, 0)
	if n != 5 || !errors.Is(err, io.EOF) || string(data[:n]) != "movie" || f.Err() != nil {
		t.Fatalf("read=%q n=%d err=%v sticky=%v", data, n, err, f.Err())
	}
	if err = f.Reauthorize(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Seek(1, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data = make([]byte, 2)
	if n, err = f.Read(data); n != 2 || err != nil || string(data) != "ov" {
		t.Fatalf("read=%q n=%d err=%v", data, n, err)
	}
}

func TestPlaybackRejectsReplacementAroundProviderOperations(t *testing.T) {
	for _, operation := range []string{"describe", "stat", "range", "second-range"} {
		for _, change := range []string{"revision", "location", "source", "installation", "generation", "artifact", "config", "runtime-config", "manifest", "installation-path", "policy"} {
			t.Run(operation+"/"+change, func(t *testing.T) {
				c, refs, registry, runtime, source, file, a := playbackFixture(t)
				registry.snapshot.Config = []*publicv1.ConfigEntry{{Key: "approved", Value: playbackConfig("original")}}
				registry.snapshot.Manifest = &publicv1.PluginManifest{PluginId: "native", Version: "1"}
				revoked := false
				a.AuthorizeSource = func(context.Context, *models.MediaFile, storagesource.SourceConfig) error {
					if revoked {
						return errors.New("policy revoked")
					}
					return nil
				}
				mutate := func() {
					switch change {
					case "revision":
						refs.ref.Revision = "same-size-new-revision"
					case "location":
						refs.ref.EntryID = "new-entry"
					case "source":
						refs.source.RootEntryID = "new-root"
						registry.snapshot.Source.RootEntryID = "new-root"
					case "generation":
						registry.snapshot.Generation++
					case "installation":
						*refs.source.InstallationID = 4
						registry.snapshot.Installation.ID = 4
					case "artifact":
						registry.snapshot.ArtifactChecksum = "new-artifact"
					case "config":
						registry.snapshot.Source.ConfigurationRevision++
						refs.source.ConfigurationRevision++
					case "runtime-config":
						registry.snapshot.Config[0].Value.Fields["fixture"] = structpb.NewStringValue("replaced")
					case "manifest":
						registry.snapshot.Manifest.Version = "2"
					case "installation-path":
						registry.snapshot.Installation.InstallPath = "/replacement/plugin"
					case "policy":
						revoked = true
					}
				}
				if operation == "describe" {
					runtime.after = mutate
				}
				if operation == "stat" {
					source.statHook = mutate
				}
				if operation == "range" {
					source.rangeHook = func(context.Context, int) { mutate() }
				}
				if operation == "second-range" {
					source.data = bytes.Repeat([]byte{'x'}, (8<<20)+1)
					source.rangeHook = func(_ context.Context, n int) {
						if n == 2 {
							mutate()
						}
					}
				}
				f, err := c.OpenAuthorizedPlayback(t.Context(), file, a)
				if operation == "describe" || operation == "stat" {
					if f != nil {
						f.Close()
					}
					if err == nil {
						t.Fatal("replacement admitted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				data := bytes.Repeat([]byte{'?'}, len(source.data))
				n, err := f.ReadAt(data, 0)
				if n != 0 || err == nil || f.Err() == nil || !bytes.Equal(data, make([]byte, len(data))) {
					t.Fatalf("exposed replaced bytes: n=%d err=%v sticky=%v", n, err, f.Err())
				}
				if n, err = f.ReadAt(data, 0); n != 0 || err == nil {
					t.Fatalf("sticky failure lost: %d %v", n, err)
				}
			})
		}
	}
}

func TestPlaybackTerminalErrorsClearWholeBuffer(t *testing.T) {
	for _, terminal := range []error{io.EOF, errors.Join(io.EOF, errors.New("revoked")), errors.New("all bytes with failure")} {
		t.Run(terminal.Error(), func(t *testing.T) {
			c, _, _, _, source, file, a := playbackFixture(t)
			source.rangeErr = terminal
			f, err := c.OpenAuthorizedPlayback(t.Context(), file, a)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			data := bytes.Repeat([]byte{'?'}, 8)
			n, err := f.ReadAt(data, 0)
			if n != 0 || !errors.Is(err, terminal) || !errors.Is(f.Err(), terminal) || !bytes.Equal(data, make([]byte, 8)) {
				t.Fatalf("terminal leak: %q %d %v %v", data, n, err, f.Err())
			}
		})
	}
}

func TestPlaybackCloseJoinsReadBeforeHostRelease(t *testing.T) {
	c, _, _, _, source, file, a := playbackFixture(t)
	host := &Host{Manager: storageplugin.NewManager(storageplugin.Config{})}
	c.AcquireOpen = host.AcquireOpen
	entered, join := make(chan struct{}), make(chan struct{})
	source.rangeHook = func(ctx context.Context, _ int) { close(entered); <-ctx.Done(); <-join }
	f, err := c.OpenAuthorizedPlayback(t.Context(), file, a)
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		data := []byte("?????")
		n, readErr := f.ReadAt(data, 0)
		if n != 0 || readErr == nil || !bytes.Equal(data, make([]byte, 5)) {
			t.Errorf("canceled read exposed bytes: %q %d %v", data, n, readErr)
		}
	}()
	<-entered
	closed := make(chan struct{})
	go func() { f.Close(); close(closed) }()
	<-f.Context().Done()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := host.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("host released before join: %v", err)
	}
	select {
	case <-closed:
		t.Fatal("Close returned before read joined")
	default:
	}
	close(join)
	<-readDone
	<-closed
	if err = host.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestPlaybackRevocationAndCancellation(t *testing.T) {
	for _, kind := range []string{"policy", "cancel", "runtime"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _, runtime, _, file, a := playbackFixture(t)
			var denied atomic.Bool
			a.AuthorizeFile = func(context.Context, *models.MediaFile) (*models.MediaFile, error) {
				if denied.Load() {
					return nil, errors.New("viewer revoked")
				}
				actual := *file
				return &actual, nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			runtime.lease.Context = ctx
			f, err := c.OpenAuthorizedPlayback(t.Context(), file, a)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			switch kind {
			case "policy":
				denied.Store(true)
			case "cancel":
				f.Cancel()
			case "runtime":
				cancel()
			}
			if err = f.Reauthorize(t.Context()); err == nil {
				t.Fatal("revocation survived")
			}
			data := []byte("?????")
			n, err := f.ReadAt(data, 0)
			if n != 0 || err == nil || f.Err() == nil || !bytes.Equal(data, make([]byte, 5)) {
				t.Fatalf("revoked read %q %d %v", data, n, err)
			}
		})
	}
}

func TestPlaybackCallerAndCallbackMutationCannotRedirectIdentity(t *testing.T) {
	c, refs, registry, runtime, _, file, a := playbackFixture(t)
	actual := *file
	a.AuthorizeFile = func(_ context.Context, argument *models.MediaFile) (*models.MediaFile, error) {
		argument.ID = 88
		argument.FilePath = "forged"
		value := actual
		return &value, nil
	}
	a.AuthorizeSource = func(_ context.Context, argument *models.MediaFile, source storagesource.SourceConfig) error {
		argument.FilePath = "forged"
		*source.InstallationID = 88
		return nil
	}
	registry.snapshot.Manifest = &publicv1.PluginManifest{PluginId: "native", Version: "1"}
	registry.snapshot.Config = []*publicv1.ConfigEntry{{Key: "approved", Value: playbackConfig("original")}}
	f, err := c.OpenAuthorizedPlayback(t.Context(), file, a)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	file.ID = 88
	file.FilePath = "forged"
	// Even the runtime receives a separate cloned snapshot.
	runtime.snapshot.Manifest.Version = "2"
	runtime.snapshot.Config[0].Value.Fields["fixture"] = structpb.NewStringValue("forged")
	identity := f.Identity()
	identity.Reference.Revision = "forged"
	identity.FileID = 88
	data := make([]byte, 5)
	n, err := f.ReadAt(data, 0)
	if n != 5 || err != nil || string(data) != "movie" || f.Identity().FileID != 7 || f.Identity().Reference.Revision != "retained-revision" || *refs.source.InstallationID != 3 {
		t.Fatalf("mutated open: %q %d %v %+v", data, n, err, f.Identity())
	}
}

func TestPlaybackRejectsInvalidAdmissionAndReleasesHost(t *testing.T) {
	for _, kind := range []string{"file-authority", "source-authority", "file-mismatch", "unpinned", "logical-path", "runtime-context", "runtime-source"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _, runtime, source, file, a := playbackFixture(t)
			host := &Host{Manager: storageplugin.NewManager(storageplugin.Config{})}
			c.AcquireOpen = host.AcquireOpen
			switch kind {
			case "file-authority":
				a.AuthorizeFile = nil
			case "source-authority":
				a.AuthorizeSource = nil
			case "file-mismatch":
				a.AuthorizeFile = func(context.Context, *models.MediaFile) (*models.MediaFile, error) {
					copy := *file
					copy.MediaFolderID = 100
					return &copy, nil
				}
			case "unpinned":
				runtime.lease.Description.Sources[0].RevisionPinnedReads = false
			case "logical-path":
				source.path = "other.mp4"
			case "runtime-context":
				runtime.lease.Context = nil
			case "runtime-source":
				runtime.lease.Source = nil
			}
			opened, err := c.OpenAuthorizedPlayback(t.Context(), file, a)
			if opened != nil {
				opened.Close()
			}
			if err == nil || opened != nil {
				t.Fatal("invalid admission accepted")
			}
			if err = host.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPlaybackCancelJoinsExplicitReauthorization(t *testing.T) {
	c, _, _, _, _, file, a := playbackFixture(t)
	var block atomic.Bool
	entered, joined := make(chan struct{}), make(chan struct{})
	a.AuthorizeFile = func(ctx context.Context, _ *models.MediaFile) (*models.MediaFile, error) {
		if block.Load() {
			close(entered)
			<-ctx.Done()
			close(joined)
			return nil, ctx.Err()
		}
		actual := *file
		return &actual, nil
	}
	f, err := c.OpenAuthorizedPlayback(t.Context(), file, a)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	block.Store(true)
	checked := make(chan error, 1)
	go func() { checked <- f.Reauthorize(context.Background()) }()
	<-entered
	f.Cancel()
	<-joined
	if err = <-checked; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPlaybackSecondRangeTerminalErrorClearsEarlierBytes(t *testing.T) {
	c, _, _, _, source, file, a := playbackFixture(t)
	source.data = bytes.Repeat([]byte{'x'}, (8<<20)+1)
	terminal := errors.Join(io.EOF, errors.New("second range revoked"))
	source.rangeHook = func(_ context.Context, n int) {
		if n == 2 {
			source.rangeErr = terminal
		}
	}
	f, err := c.OpenAuthorizedPlayback(t.Context(), file, a)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data := bytes.Repeat([]byte{'?'}, len(source.data)+5)
	n, err := f.Read(data)
	if n != 0 || !errors.Is(err, terminal) || !bytes.Equal(data, make([]byte, len(data))) {
		t.Fatalf("earlier bytes exposed: %d %v", n, err)
	}
}
