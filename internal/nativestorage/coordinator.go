// Package nativestorage composes host authorization with the private storage runtime.
package nativestorage

import (
	"context"
	"errors"
	"sync"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

type References interface {
	FileReference(context.Context, int, int) (storagesource.SourceConfig, storagesource.PersistedRef, error)
}
type Registry interface {
	Snapshot(context.Context, uuid.UUID, uuid.UUID) (*plugins.NativeStorageSnapshot, error)
}
type Lease struct {
	Context context.Context
	Source  mediasource.Source
}
type Runtime interface {
	Borrow(context.Context, storageplugin.Snapshot) (Lease, error)
	Disable(int)
}
type ManagedRuntime struct{ Manager *storageplugin.Manager }

func (r ManagedRuntime) Borrow(ctx context.Context, snapshot storageplugin.Snapshot) (Lease, error) {
	s, err := r.Manager.Ensure(ctx, snapshot)
	if err != nil {
		return Lease{}, err
	}
	return Lease{Context: s.Context(), Source: mediasource.NewPluginSource(s.Provider())}, nil
}
func (r ManagedRuntime) Disable(id int) { r.Manager.Disable(id) }

// Coordinator requires real host authorizers. Registry owner equality alone is
// never authority. Keep one coordinator per manager to serialize local mutation.
type Coordinator struct {
	References      References
	Registry        Registry
	Runtime         Runtime
	AcquireOpen     func() (func(), error)
	AuthorizeFile   func(context.Context, *models.MediaFile) (*models.MediaFile, error)
	AuthorizeSource func(context.Context, *models.MediaFile, storagesource.SourceConfig) error
	mu              sync.RWMutex
}

func (c *Coordinator) resolve(ctx context.Context, file *models.MediaFile) (storagesource.SourceConfig, storagesource.PersistedRef, *plugins.NativeStorageSnapshot, error) {
	var source storagesource.SourceConfig
	var ref storagesource.PersistedRef
	actual, err := c.AuthorizeFile(ctx, file)
	if err != nil {
		return source, ref, nil, err
	}
	if actual == nil || actual.ID != file.ID || actual.ContentID != file.ContentID || actual.MediaFolderID != file.MediaFolderID || actual.FilePath != file.FilePath {
		return source, ref, nil, storagesource.ErrReferenceConflict
	}
	source, ref, err = c.References.FileReference(ctx, actual.ID, actual.MediaFolderID)
	if err != nil {
		return source, ref, nil, err
	}
	location, err := storagesource.CatalogLocation(ref.LocationID, ref.EntryID)
	if err != nil || location != actual.FilePath {
		return source, ref, nil, storagesource.ErrReferenceConflict
	}
	if !source.Enabled || source.InstallationID == nil {
		return source, ref, nil, storagesource.ErrSourceUnavailable
	}
	if err = c.AuthorizeSource(ctx, actual, source); err != nil {
		return source, ref, nil, err
	}
	snapshot, err := c.Registry.Snapshot(ctx, source.Key, source.OwnerID)
	if err != nil {
		return source, ref, nil, err
	}
	if snapshot == nil || snapshot.Installation == nil || !sameSource(source, snapshot.Source) || int64(snapshot.Installation.ID) != *source.InstallationID || !snapshot.Installation.Enabled {
		return source, ref, nil, storagesource.ErrSourceUnavailable
	}
	return source, ref, snapshot, nil
}
func sameSource(a, b storagesource.SourceConfig) bool {
	return a.Key == b.Key && a.OwnerID == b.OwnerID && a.InstallationID != nil && b.InstallationID != nil && *a.InstallationID == *b.InstallationID && a.PluginID == b.PluginID && a.ProviderSourceID == b.ProviderSourceID && a.RootEntryID == b.RootEntryID && a.ConfigurationRevision == b.ConfigurationRevision && a.Enabled == b.Enabled
}
func (c *Coordinator) OpenAuthorizedEbook(ctx context.Context, file *models.MediaFile) (mediasource.File, error) {
	if c == nil || file == nil || c.References == nil || c.Registry == nil || c.Runtime == nil || c.AuthorizeFile == nil || c.AuthorizeSource == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	release := func() {}
	if c.AcquireOpen != nil {
		var err error
		release, err = c.AcquireOpen()
		if err != nil {
			return nil, err
		}
	}
	handedOff := false
	defer func() {
		if !handedOff {
			release()
		}
	}()
	c.mu.RLock()
	defer c.mu.RUnlock()
	source, ref, snapshot, err := c.resolve(ctx, file)
	if err != nil {
		return nil, err
	}
	lease, err := c.Runtime.Borrow(ctx, storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: snapshot.Generation, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: snapshot.Manifest, Config: snapshot.Config, Enabled: true, NativeOnly: true})
	if err != nil {
		return nil, err
	}
	// Startup can be slow. Never open using a view read before a remote mutation.
	fresh, freshRef, freshSnapshot, err := c.resolve(ctx, file)
	if err != nil {
		return nil, err
	}
	if !sameSource(source, fresh) || ref != freshRef || freshSnapshot.Generation != snapshot.Generation || freshSnapshot.ArtifactChecksum != snapshot.ArtifactChecksum {
		return nil, storagesource.ErrSourceUnavailable
	}
	if lease.Context == nil || lease.Source == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	readCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lease.Context, cancel)
	if lease.Context.Err() != nil {
		cancel()
	}
	opened, err := mediasource.Open(readCtx, lease.Source, mediasource.Ref{SourceID: source.ProviderSourceID, EntryID: ref.EntryID, Revision: ref.Revision})
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	if opened.Info().LogicalPath != ref.LogicalPath {
		opened.Close()
		stop()
		cancel()
		return nil, storagesource.ErrReferenceConflict
	}
	handedOff = true
	return &lifetimeFile{File: opened, cancel: cancel, stop: stop, release: release}, nil
}

type lifetimeFile struct {
	mediasource.File
	cancel  context.CancelFunc
	release func()
	stop    func() bool
	once    sync.Once
	err     error
}

func (f *lifetimeFile) Close() error {
	f.once.Do(func() {
		f.stop()
		f.cancel()
		f.err = f.File.Close()
		if f.release != nil {
			f.release()
		}
	})
	return f.err
}

// FenceMutation is only for future independently authorized host lifecycle calls,
// never request dispatch. Disable before AND after the commit attempt, including
// ambiguous failures: availability requires a fresh higher durable generation.
// Keep the registry alive until runtime Shutdown has reaped disabled processes.
func (c *Coordinator) FenceMutation(ctx context.Context, id int, mutation func(context.Context) error) error {
	if c == nil || c.Runtime == nil || id <= 0 || mutation == nil {
		return errors.New("invalid native lifecycle mutation")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Runtime.Disable(id)
	defer c.Runtime.Disable(id)
	return mutation(ctx)
}
