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
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

type References interface {
	FileReference(context.Context, int, int) (storagesource.SourceConfig, storagesource.PersistedRef, error)
	CoverReference(context.Context, string, string) (storagesource.CoverReference, error)
}
type Registry interface {
	Snapshot(context.Context, uuid.UUID, uuid.UUID) (*plugins.NativeStorageSnapshot, error)
}
type Lease struct {
	Context  context.Context
	Provider storagev1.StorageProviderClient
	Source   mediasource.Source
	// Description is a fresh, completed response owned by this borrower.
	Description *storagev1.DescribeResponse
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
	description, err := storageplugin.Describe(ctx, s.Provider())
	if err != nil {
		return Lease{}, err
	}
	return Lease{Context: s.Context(), Provider: s.Provider(), Source: mediasource.NewPluginSource(s.Provider()), Description: description}, nil
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
	// AuthorizeCover checks the library's current source authority in addition
	// to the viewer-bound publication checks carried by the request context.
	AuthorizeCover func(context.Context, storagesource.Location, storagesource.SourceConfig) error
	mu             sync.RWMutex
}

// SameSource compares retained identity and configuration, without granting
// authority. Callers must separately recheck current ownership and policy.
func SameSource(a, b storagesource.SourceConfig) bool {
	return a.Key == b.Key && a.OwnerID == b.OwnerID && a.InstallationID != nil && b.InstallationID != nil && *a.InstallationID == *b.InstallationID && a.PluginID == b.PluginID && a.ProviderSourceID == b.ProviderSourceID && a.RootEntryID == b.RootEntryID && a.ConfigurationRevision == b.ConfigurationRevision && a.Enabled == b.Enabled
}
func (c *Coordinator) OpenAuthorizedEbook(ctx context.Context, file *models.MediaFile) (mediasource.File, error) {
	if c == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	opened, err := c.OpenAuthorizedPlayback(ctx, file, PlaybackAuthority{AuthorizeFile: func(ctx context.Context, file *models.MediaFile) (*models.MediaFile, error) {
		if c.AuthorizeFile == nil {
			return nil, storagesource.ErrSourceUnavailable
		}
		return c.AuthorizeFile(ctx, file)
	}, AuthorizeSource: func(ctx context.Context, file *models.MediaFile, source storagesource.SourceConfig) error {
		if c.AuthorizeSource == nil {
			return storagesource.ErrSourceUnavailable
		}
		return c.AuthorizeSource(ctx, file, source)
	}})
	if err != nil {
		return nil, err
	}
	return opened, nil
}

// OpenCover opens the cover a book's storage source offers, pinned to the
// revision its artwork URL names. Host reads carry a viewer-bound publication
// authorizer; both viewer and source authority are checked around provider I/O.
func (c *Coordinator) OpenCover(ctx context.Context, contentID, revision string) (mediasource.File, error) {
	if c == nil || c.References == nil || c.Registry == nil || c.Runtime == nil || c.AuthorizeCover == nil {
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
	ref, snapshot, err := c.resolveCover(ctx, contentID, revision)
	if err != nil {
		return nil, err
	}
	lease, err := c.Runtime.Borrow(ctx, storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: snapshot.Generation, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: snapshot.Manifest, Config: snapshot.Config, Enabled: true, NativeOnly: true})
	if err != nil {
		return nil, err
	}
	if err = ValidateReadDescription(lease.Description, ref.Source.ProviderSourceID, ref.Source.RootEntryID, snapshot); err != nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	if err = c.recheckCover(ctx, contentID, revision, ref, snapshot); err != nil {
		return nil, err
	}
	if lease.Context == nil || lease.Source == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = lease.Context.Err(); err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithCancelCause(ctx)
	state := &playbackFileState{ctx: readCtx, cancel: cancel, coordinator: c, source: clonePlaybackSource(ref.Source), ref: ref.Cover, snapshot: clonePlaybackSnapshot(snapshot), runtime: lease.Context, release: release, cover: true}
	state.checkAuthority = func(ctx context.Context) error {
		return c.recheckCover(ctx, contentID, revision, ref, state.snapshot)
	}
	state.stop = context.AfterFunc(lease.Context, func() { _ = state.fail(storageplugin.ErrUnavailable) })
	defer func() {
		if !handedOff {
			state.stop()
			cancel(context.Canceled)
		}
	}()
	if err = state.checkLocked(readCtx); err != nil {
		return nil, err
	}
	providerRef := mediasource.Ref{SourceID: ref.Source.ProviderSourceID, EntryID: ref.Cover.EntryID, Revision: ref.Cover.Revision}
	opened, err := mediasource.Open(readCtx, &playbackSource{raw: lease.Source, state: state, ref: providerRef}, providerRef)
	if err != nil {
		return nil, err
	}
	handedOff = true
	return &PlaybackFile{file: opened, state: state}, nil
}

func (c *Coordinator) resolveCover(ctx context.Context, contentID, revision string) (storagesource.CoverReference, *plugins.NativeStorageSnapshot, error) {
	ref, err := resolveCoverRead(ctx, c.References, contentID, revision)
	if err != nil {
		return ref, nil, err
	}
	if !ref.Source.Enabled || ref.Source.InstallationID == nil || ref.Location.SourceKey != ref.Source.Key || ref.Cover.LocationID != ref.Location.ID {
		return ref, nil, storagesource.ErrSourceUnavailable
	}
	if err = c.AuthorizeCover(ctx, ref.Location, ref.Source); err != nil {
		return ref, nil, err
	}
	snapshot, err := c.Registry.Snapshot(ctx, ref.Source.Key, ref.Source.OwnerID)
	if err != nil {
		return ref, nil, err
	}
	if snapshot == nil || snapshot.Installation == nil || !SameSource(ref.Source, snapshot.Source) || int64(snapshot.Installation.ID) != *ref.Source.InstallationID || !snapshot.Installation.Enabled {
		return ref, nil, storagesource.ErrSourceUnavailable
	}
	return ref, clonePlaybackSnapshot(snapshot), nil
}

// recheckCover revalidates a pinned cover publication. It finishes the pinned
// source's registry/configuration wait before repeating library-source,
// publication and viewer authority, so a revocation committed during that
// lookup is seen before bytes are delivered or cached bytes are returned.
func (c *Coordinator) recheckCover(ctx context.Context, contentID, revision string, pinned storagesource.CoverReference, snapshot *plugins.NativeStorageSnapshot) error {
	current, err := c.Registry.Snapshot(ctx, pinned.Source.Key, pinned.Source.OwnerID)
	if err != nil {
		return err
	}
	if !sameScanSnapshot(snapshot, current) {
		return storagesource.ErrSourceUnavailable
	}
	if err = c.AuthorizeCover(ctx, pinned.Location, clonePlaybackSource(pinned.Source)); err != nil {
		return err
	}
	fresh, err := resolveCoverRead(ctx, c.References, contentID, revision)
	if err != nil {
		return err
	}
	if !SameCoverReference(pinned, fresh) {
		return storagesource.ErrReferenceConflict
	}
	return ctx.Err()
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
