package nativestorage

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// PlaybackAuthority is host viewer/session policy for one open. Both callbacks
// must recheck current authority on every call, complete DB work before
// returning, and honor cancellation. Scan or ebook authority is no substitute.
type PlaybackAuthority struct {
	AuthorizeFile   func(context.Context, *models.MediaFile) (*models.MediaFile, error)
	AuthorizeSource func(context.Context, *models.MediaFile, storagesource.SourceConfig) error
}

// PlaybackIdentity is a frozen, pointer-free catalog and runtime descriptor.
// It grants no authority and contains no provider URL or secret configuration.
// A session reopening a file must compare this entire value before reading.
type PlaybackIdentity struct {
	FileID, MediaFolderID                   int
	ContentID, FilePath                     string
	SourceKey, OwnerID                      uuid.UUID
	InstallationID                          int
	PluginID, ProviderSourceID, RootEntryID string
	ConfigurationRevision                   int64
	Reference                               storagesource.PersistedRef
	Generation                              uint64
	ArtifactChecksum                        string
}

func clonePlaybackSource(source storagesource.SourceConfig) storagesource.SourceConfig {
	if source.InstallationID != nil {
		id := *source.InstallationID
		source.InstallationID = &id
	}
	return source
}
func clonePlaybackSnapshot(snapshot *plugins.NativeStorageSnapshot) *plugins.NativeStorageSnapshot {
	frozen := *snapshot
	installation := *snapshot.Installation
	frozen.Installation = &installation
	frozen.Source = clonePlaybackSource(snapshot.Source)
	frozen.Manifest = proto.CloneOf(snapshot.Manifest)
	frozen.Config = proto.CloneOf(&publicv1.ConfigureRequest{Config: snapshot.Config}).Config
	return &frozen
}

// resolvePlayback never substitutes or changes the ebook authorizer fields.
func (c *Coordinator) resolvePlayback(ctx context.Context, file models.MediaFile, authority PlaybackAuthority) (storagesource.SourceConfig, storagesource.PersistedRef, *plugins.NativeStorageSnapshot, error) {
	var source storagesource.SourceConfig
	var ref storagesource.PersistedRef
	argument := file
	actual, err := authority.AuthorizeFile(ctx, &argument)
	if err != nil {
		return source, ref, nil, err
	}
	if actual == nil || actual.ID != file.ID || actual.ContentID != file.ContentID || actual.EpisodeID != file.EpisodeID || actual.ExtraID != file.ExtraID || actual.MediaFolderID != file.MediaFolderID || actual.FilePath != file.FilePath {
		return source, ref, nil, storagesource.ErrReferenceConflict
	}
	actualFile := *actual
	source, ref, err = c.References.FileReference(ctx, actualFile.ID, actualFile.MediaFolderID)
	if err != nil {
		return source, ref, nil, err
	}
	source = clonePlaybackSource(source)
	location, err := storagesource.CatalogLocation(ref.LocationID, ref.EntryID)
	if err != nil || location != actualFile.FilePath {
		return source, ref, nil, storagesource.ErrReferenceConflict
	}
	if !source.Enabled || source.InstallationID == nil {
		return source, ref, nil, storagesource.ErrSourceUnavailable
	}
	if err = authority.AuthorizeSource(ctx, &actualFile, clonePlaybackSource(source)); err != nil {
		return source, ref, nil, err
	}
	snapshot, err := c.Registry.Snapshot(ctx, source.Key, source.OwnerID)
	if err != nil {
		return source, ref, nil, err
	}
	if snapshot == nil || snapshot.Installation == nil || !SameSource(source, snapshot.Source) || int64(snapshot.Installation.ID) != *source.InstallationID || !snapshot.Installation.Enabled {
		return source, ref, nil, storagesource.ErrSourceUnavailable
	}
	if err = ctx.Err(); err != nil {
		return source, ref, nil, err
	}
	return source, ref, clonePlaybackSnapshot(snapshot), nil
}

// OpenAuthorizedPlayback admits only an already selected host catalog file.
// Host ownership lasts until Close joins I/O. The shared mutation fence covers
// local lifecycle changes; fresh policy and reference/snapshot reads around
// provider I/O detect durable changes from other hosts. No DB transaction spans I/O.
func (c *Coordinator) OpenAuthorizedPlayback(ctx context.Context, file *models.MediaFile, authority PlaybackAuthority) (*PlaybackFile, error) {
	if ctx == nil || c == nil || file == nil || c.References == nil || c.Registry == nil || c.Runtime == nil || authority.AuthorizeFile == nil || authority.AuthorizeSource == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	// Retain only addressing values. A fresh callback argument cannot rewrite them.
	frozenFile := models.MediaFile{ID: file.ID, ContentID: file.ContentID, EpisodeID: file.EpisodeID, ExtraID: file.ExtraID, MediaFolderID: file.MediaFolderID, FilePath: file.FilePath}
	release := func() {}
	if c.AcquireOpen != nil {
		var err error
		release, err = c.AcquireOpen()
		if err != nil {
			return nil, err
		}
		if release == nil {
			return nil, storagesource.ErrSourceUnavailable
		}
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	c.mu.RLock()
	defer c.mu.RUnlock()
	source, ref, snapshot, err := c.resolvePlayback(ctx, frozenFile, authority)
	if err != nil {
		return nil, err
	}
	borrowed := clonePlaybackSnapshot(snapshot)
	lease, err := c.Runtime.Borrow(ctx, storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: snapshot.Generation, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: borrowed.Manifest, Config: borrowed.Config, Enabled: true, NativeOnly: true})
	if err != nil {
		return nil, err
	}
	if err = ValidateReadDescription(lease.Description, source.ProviderSourceID, source.RootEntryID, snapshot); err != nil || lease.Context == nil || lease.Source == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	readCtx, cancel := context.WithCancelCause(ctx)
	state := &playbackFileState{ctx: readCtx, cancel: cancel, coordinator: c, authority: authority, file: frozenFile, source: source, ref: ref, snapshot: snapshot, runtime: lease.Context, release: release}
	state.stop = context.AfterFunc(lease.Context, func() { _ = state.fail(storageplugin.ErrUnavailable) })
	defer func() {
		if !transferred {
			state.stop()
			cancel(context.Canceled)
		}
	}()
	// Slow startup/Describe must not leave a stale pre-start authority view.
	if err = state.checkLocked(readCtx); err != nil {
		return nil, err
	}
	providerRef := mediasource.Ref{SourceID: source.ProviderSourceID, EntryID: ref.EntryID, Revision: ref.Revision}
	opened, err := mediasource.Open(readCtx, &playbackSource{raw: lease.Source, state: state, ref: providerRef}, providerRef)
	if err != nil {
		return nil, err
	}
	transferred = true
	return &PlaybackFile{file: opened, state: state, identity: PlaybackIdentity{FileID: frozenFile.ID, MediaFolderID: frozenFile.MediaFolderID, ContentID: frozenFile.ContentID, FilePath: frozenFile.FilePath, SourceKey: source.Key, OwnerID: source.OwnerID, InstallationID: snapshot.Installation.ID, PluginID: source.PluginID, ProviderSourceID: source.ProviderSourceID, RootEntryID: source.RootEntryID, ConfigurationRevision: source.ConfigurationRevision, Reference: ref, Generation: snapshot.Generation, ArtifactChecksum: snapshot.ArtifactChecksum}}, nil
}

// PlaybackFile owns admission until Close. Cancellation and terminal policy or
// source failures stop work without releasing ownership of active operations.
type PlaybackFile struct {
	file     mediasource.File
	state    *playbackFileState
	identity PlaybackIdentity
	once     sync.Once
	closeErr error
}

var _ mediasource.File = (*PlaybackFile)(nil)

type playbackFileState struct {
	ctx         context.Context
	cancel      context.CancelCauseFunc
	coordinator *Coordinator
	authority   PlaybackAuthority
	file        models.MediaFile
	source      storagesource.SourceConfig
	ref         storagesource.PersistedRef
	snapshot    *plugins.NativeStorageSnapshot
	runtime     context.Context
	stop        func() bool
	release     func()
	mu          sync.Mutex
	closed      bool
	sticky      error
	active      sync.WaitGroup
}

func (s *playbackFileState) fail(err error) error {
	if err == nil {
		return nil
	}
	s.mu.Lock()
	if s.sticky == nil {
		s.sticky = err
	}
	sticky := s.sticky
	s.mu.Unlock()
	s.cancel(sticky)
	return sticky
}
func (s *playbackFileState) err() error {
	s.mu.Lock()
	sticky := s.sticky
	s.mu.Unlock()
	if sticky != nil {
		return sticky
	}
	if s.runtime.Err() != nil {
		return s.fail(storageplugin.ErrUnavailable)
	}
	if s.ctx.Err() != nil {
		return s.fail(context.Cause(s.ctx))
	}
	return nil
}
func (s *playbackFileState) enter() error {
	if err := s.err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sticky != nil {
		return s.sticky
	}
	if s.closed {
		return os.ErrClosed
	}
	s.active.Add(1)
	return nil
}

// Caller holds coordinator.mu.RLock. Fresh reads finish before provider I/O.
func (s *playbackFileState) checkLocked(ctx context.Context) error {
	if err := s.err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return s.fail(context.Cause(ctx))
	}
	source, ref, snapshot, err := s.coordinator.resolvePlayback(ctx, s.file, s.authority)
	// The scan reader's pure snapshot equality also compares cloned config,
	// manifest, installation version/path and enabled state, not just generation.
	if err == nil && (!SameSource(source, s.source) || ref != s.ref || !sameScanSnapshot(s.snapshot, snapshot)) {
		err = storagesource.ErrSourceUnavailable
	}
	if err != nil {
		return s.fail(err)
	}
	return s.err()
}
func (s *playbackFileState) check(ctx context.Context) error {
	s.coordinator.mu.RLock()
	defer s.coordinator.mu.RUnlock()
	return s.checkLocked(ctx)
}
func (f *PlaybackFile) Identity() PlaybackIdentity { return f.identity }
func (f *PlaybackFile) Info() mediasource.Info     { return f.file.Info() }
func (f *PlaybackFile) Context() context.Context   { return f.state.ctx }
func (f *PlaybackFile) Cancel()                    { _ = f.state.fail(context.Canceled) }
func (f *PlaybackFile) Err() error                 { return f.state.err() }
func (f *PlaybackFile) Reauthorize(ctx context.Context) error {
	if ctx == nil {
		return f.state.fail(storagesource.ErrSourceUnavailable)
	}
	if err := f.state.enter(); err != nil {
		return err
	}
	defer f.state.active.Done()
	// Cancel must also interrupt a callback using an explicit check context.
	checkCtx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(f.state.ctx, func() { cancel(context.Cause(f.state.ctx)) })
	defer stop()
	defer cancel(context.Canceled)
	return f.state.check(checkCtx)
}
func (f *PlaybackFile) ReadAt(data []byte, offset int64) (int, error) {
	if err := f.state.enter(); err != nil {
		clear(data)
		return 0, err
	}
	defer f.state.active.Done()
	if err := f.state.check(f.state.ctx); err != nil {
		clear(data)
		return 0, err
	}
	n, err := f.file.ReadAt(data, offset)
	return f.finishRead(data, n, err)
}
func (f *PlaybackFile) Read(data []byte) (int, error) {
	if err := f.state.enter(); err != nil {
		clear(data)
		return 0, err
	}
	defer f.state.active.Done()
	if err := f.state.check(f.state.ctx); err != nil {
		clear(data)
		return 0, err
	}
	n, err := f.file.Read(data)
	return f.finishRead(data, n, err)
}
func (f *PlaybackFile) finishRead(data []byte, n int, err error) (int, error) {
	if sticky := f.Err(); sticky != nil {
		clear(data)
		return 0, sticky
	}
	// Only exact EOF synthesized at the known file size is normal. Any provider
	// EOF, including joined EOF, has already made the source failure sticky.
	if err != nil && !errors.Is(err, io.EOF) {
		clear(data)
		return 0, f.state.fail(err)
	}
	return n, err
}
func (f *PlaybackFile) Seek(offset int64, whence int) (int64, error) {
	if err := f.state.enter(); err != nil {
		return 0, err
	}
	defer f.state.active.Done()
	if err := f.state.check(f.state.ctx); err != nil {
		return 0, err
	}
	return f.file.Seek(offset, whence)
}
func (f *PlaybackFile) Close() error {
	f.once.Do(func() {
		f.state.mu.Lock()
		f.state.closed = true
		f.state.mu.Unlock()
		f.state.cancel(context.Canceled)
		f.state.stop()
		f.state.active.Wait()
		f.closeErr = f.file.Close()
		f.state.release()
	})
	return f.closeErr
}

type playbackSource struct {
	raw   mediasource.Source
	state *playbackFileState
	ref   mediasource.Ref
}

// Stat runs while Open holds the coordinator fence.
func (s *playbackSource) Stat(ctx context.Context, ref mediasource.Ref) (mediasource.Info, error) {
	if ref != s.ref {
		return mediasource.Info{}, s.state.fail(storagesource.ErrReferenceConflict)
	}
	if err := s.state.checkLocked(ctx); err != nil {
		return mediasource.Info{}, err
	}
	info, err := s.raw.Stat(ctx, ref)
	after := s.state.checkLocked(ctx)
	if err = errors.Join(err, after); err != nil {
		return mediasource.Info{}, s.state.fail(err)
	}
	if info.LogicalPath != s.state.ref.LogicalPath || info.Revision != s.ref.Revision {
		return mediasource.Info{}, s.state.fail(storagesource.ErrReferenceConflict)
	}
	return info, nil
}
func (s *playbackSource) ReadRange(ctx context.Context, ref mediasource.Ref, offset, length int64, w io.Writer) error {
	if ref != s.ref {
		return s.state.fail(storagesource.ErrReferenceConflict)
	}
	s.state.coordinator.mu.RLock()
	defer s.state.coordinator.mu.RUnlock()
	if err := s.state.checkLocked(ctx); err != nil {
		return err
	}
	err := s.raw.ReadRange(ctx, ref, offset, length, w)
	after := s.state.checkLocked(ctx)
	return s.state.fail(errors.Join(err, after))
}
