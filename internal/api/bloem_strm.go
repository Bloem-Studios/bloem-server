package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"sync"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/remotesource"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// `.strm` items play through Bloem's native video pipeline: the catalog file
// is a native location (storagesource.IsNativeLocation), and the file that
// NativeVideoService.Open and OpenDelegated return reads the remote URL with
// range requests through remotesource's guard. Direct play, remux, HLS on the
// API server, subtitle extraction and recovery then work unchanged, and the
// URL never reaches a client, a recipe or a stream token. Every reauthorize
// rechecks the viewer and the catalog file, and that the recorded URL is still
// the one this playback bound; a changed URL is a new revision.

type strmSources interface {
	Get(context.Context, string) (remotesource.Source, bool, error)
}

type strmPlayback struct {
	sources strmSources
	guard   *remotesource.Guard
}

func newStrmPlayback(deps Dependencies) *strmPlayback {
	rt := remotesource.ForPool(deps.DB)
	if rt == nil {
		return nil
	}
	return &strmPlayback{sources: rt.Store, guard: rt.Guard}
}

// nativeStorageOpen opens a storage source's file under the viewer's authority.
type nativeStorageOpen func(context.Context, *models.MediaFile, nativestorage.PlaybackAuthority) (handlers.NativeVideoFile, error)

func coordinatorOpen(coordinator *nativestorage.Coordinator) nativeStorageOpen {
	return func(ctx context.Context, file *models.MediaFile, authority nativestorage.PlaybackAuthority) (handlers.NativeVideoFile, error) {
		opened, err := coordinator.OpenAuthorizedPlayback(ctx, file, authority)
		if err != nil {
			return nil, err
		}
		return opened, nil
	}
}

// open returns the `.strm` file's remote reader when the catalog path has a
// remote source, else the storage source's own file. A local `.strm` without
// a recorded source is not playable.
func (s *strmPlayback) open(ctx context.Context, file *models.MediaFile, authority nativestorage.PlaybackAuthority, storage nativeStorageOpen) (handlers.NativeVideoFile, error) {
	if file == nil {
		return nil, catalog.ErrItemNotFound
	}
	local := remotesource.IsStrmPath(file.FilePath)
	var (
		src   remotesource.Source
		found bool
		err   error
	)
	if s != nil && s.sources != nil && s.guard != nil {
		src, found, err = s.sources.Get(ctx, file.FilePath)
		if err != nil {
			return nil, err
		}
	}
	switch {
	case found && local:
		return s.openLocal(ctx, file, src, authority)
	case found:
		return s.openStored(ctx, file, src, authority, storage)
	case local:
		return nil, catalog.ErrItemNotFound
	default:
		return storage(ctx, file, authority)
	}
}

func sameCatalogFile(a, b *models.MediaFile) bool {
	return a != nil && b != nil && a.ID == b.ID && a.FilePath == b.FilePath && a.MediaFolderID == b.MediaFolderID && a.ContentID == b.ContentID
}

// sameSource fails once the recorded URL is no longer the bound one.
func (s *strmPlayback) sameSource(ctx context.Context, filePath string, bound remotesource.Source) error {
	current, ok, err := s.sources.Get(ctx, filePath)
	if err != nil {
		return err
	}
	if !ok || current.Revision != bound.Revision || current.SourceRevision != bound.SourceRevision {
		return storagesource.ErrReferenceConflict
	}
	return nil
}

func (s *strmPlayback) openLocal(ctx context.Context, file *models.MediaFile, src remotesource.Source, authority nativestorage.PlaybackAuthority) (handlers.NativeVideoFile, error) {
	if authority.AuthorizeFile == nil {
		return nil, catalog.ErrItemNotFound
	}
	current, err := authority.AuthorizeFile(ctx, file)
	if err != nil {
		return nil, err
	}
	if !sameCatalogFile(current, file) {
		return nil, storagesource.ErrReferenceConflict
	}
	lifetime, cancel := context.WithCancel(ctx)
	remote, err := remotesource.Open(lifetime, s.guard.Client(), src.URL, remotesource.NameHint(src.URL))
	if err != nil {
		cancel()
		return nil, strmOpenError(err)
	}
	frozen := *file
	return &strmVideoFile{
		remote:   remote,
		identity: nativestorage.PlaybackIdentity{FileID: file.ID, MediaFolderID: file.MediaFolderID, ContentID: file.ContentID, FilePath: file.FilePath, Reference: storagesource.PersistedRef{Revision: src.Revision, LogicalPath: file.FilePath}},
		info:     mediasource.Info{Name: path.Base(file.FilePath), LogicalPath: file.FilePath, Revision: src.Revision, Size: remote.Info().Size},
		ctx:      lifetime,
		cancel:   cancel,
		check: func(check context.Context) error {
			current, err := authority.AuthorizeFile(check, &frozen)
			if err != nil {
				return err
			}
			if !sameCatalogFile(current, &frozen) {
				return storagesource.ErrReferenceConflict
			}
			return s.sameSource(check, frozen.FilePath, src)
		},
	}, nil
}

// openStored opens the `.strm` object through the storage source with every
// usual check, and requires the object revision the scan read the URL from.
func (s *strmPlayback) openStored(ctx context.Context, file *models.MediaFile, src remotesource.Source, authority nativestorage.PlaybackAuthority, storage nativeStorageOpen) (handlers.NativeVideoFile, error) {
	if storage == nil {
		return nil, catalog.ErrItemNotFound
	}
	base, err := storage(ctx, file, authority)
	if err != nil {
		return nil, err
	}
	identity := base.Identity()
	if src.SourceRevision == "" || src.SourceRevision != identity.Reference.Revision {
		_ = base.Close()
		return nil, storagesource.ErrReferenceConflict
	}
	remote, err := remotesource.Open(base.Context(), s.guard.Client(), src.URL, remotesource.NameHint(src.URL))
	if err != nil {
		_ = base.Close()
		return nil, strmOpenError(err)
	}
	info := base.Info()
	info.Size = remote.Info().Size
	return &strmVideoFile{
		remote:   remote,
		base:     base,
		identity: identity,
		info:     info,
		ctx:      base.Context(),
		cancel:   base.Cancel,
		check: func(check context.Context) error {
			if err := base.Reauthorize(check); err != nil {
				return err
			}
			return s.sameSource(check, file.FilePath, src)
		},
	}, nil
}

// strmVideoFile is a handlers.NativeVideoFile whose bytes come from the
// remote URL and whose authority comes from the catalog (and, for a storage
// object, the storage source).
type strmVideoFile struct {
	remote   *remotesource.File
	base     handlers.NativeVideoFile
	identity nativestorage.PlaybackIdentity
	info     mediasource.Info
	ctx      context.Context //nolint:containedctx // NativeVideoFile.Context reports the open file's lifetime
	cancel   context.CancelFunc
	check    func(context.Context) error
	once     sync.Once
}

var _ handlers.NativeVideoFile = (*strmVideoFile)(nil)

func (f *strmVideoFile) Read(p []byte) (int, error) { return f.remote.Read(p) }
func (f *strmVideoFile) ReadAt(p []byte, offset int64) (int, error) {
	return f.remote.ReadAt(p, offset)
}
func (f *strmVideoFile) Seek(offset int64, whence int) (int64, error) {
	return f.remote.Seek(offset, whence)
}
func (f *strmVideoFile) Info() mediasource.Info                   { return f.info }
func (f *strmVideoFile) Context() context.Context                 { return f.ctx }
func (f *strmVideoFile) Identity() nativestorage.PlaybackIdentity { return f.identity }

func (f *strmVideoFile) Cancel() {
	f.remote.Cancel()
	f.cancel()
}

func (f *strmVideoFile) Err() error {
	if err := f.remote.Err(); err != nil {
		return err
	}
	if f.base != nil {
		if err := f.base.Err(); err != nil {
			return err
		}
	}
	return f.ctx.Err()
}

func (f *strmVideoFile) Reauthorize(ctx context.Context) error {
	if err := f.Err(); err != nil {
		return err
	}
	return f.check(ctx)
}

func (f *strmVideoFile) Close() error {
	var err error
	f.once.Do(func() {
		err = f.remote.Close()
		if f.base != nil {
			err = errors.Join(err, f.base.Close())
		} else {
			f.cancel()
		}
	})
	return err
}

var _ io.ReadSeeker = (*strmVideoFile)(nil)

// strmRefusedError is an open the guard refused. It stays
// remotesource.ErrBlockedAddress for callers that check it, and reads as a
// 403 API error, which the Jellyfin native media host answers with: the
// address is refused, the service is not down. It never names the URL.
type strmRefusedError struct{ err error }

func (e strmRefusedError) Error() string { return e.err.Error() }
func (e strmRefusedError) Unwrap() error { return e.err }
func (e strmRefusedError) As(target any) bool {
	if apiErr, ok := target.(**handlers.APIError); ok {
		*apiErr = &handlers.APIError{Status: http.StatusForbidden, Code: "source_refused", Message: "This video's address is refused by the server"}
		return true
	}
	return false
}

func strmOpenError(err error) error {
	if errors.Is(err, remotesource.ErrBlockedAddress) {
		return strmRefusedError{err: err}
	}
	return err
}
